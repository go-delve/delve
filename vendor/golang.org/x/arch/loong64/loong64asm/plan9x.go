// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loong64asm

import (
	"fmt"
	"strings"
)

// GoSyntax returns the Go assembler syntax for the instruction.
// The syntax was originally defined by Plan 9.
// The pc is the program counter of the instruction, used for
// expanding PC-relative addresses into absolute ones.
// The symname function queries the symbol table for the program
// being disassembled. Given a target address it returns the name
// and base address of the symbol containing the target, if any;
// otherwise it returns "", 0.
func GoSyntax(inst Inst, pc uint64, symname func(uint64) (string, uint64)) string {
	if symname == nil {
		symname = func(uint64) (string, uint64) { return "", 0 }
	}
	if inst.Op == 0 && inst.Enc == 0 {
		return "WORD $0"
	} else if inst.Op == 0 {
		return "?"
	}

	var args []string
	for _, a := range inst.Args {
		if a == nil {
			break
		}
		args = append(args, plan9Arg(&inst, pc, symname, a))
	}

	var op string = plan9OpMap[inst.Op]
	if op == "" {
		op = "Unknown " + inst.Op.String()
	}

	switch inst.Op {
	case BSTRPICK_D:
		msbw, lsbw := inst.Args[2].(Uimm), inst.Args[3].(Uimm)
		if msbw.Imm == 15 && lsbw.Imm == 0 {
			op = "MOVHU"
		} else if msbw.Imm == 31 && lsbw.Imm == 0 {
			op = "MOVWU"
		}
		args = append(args[1:2], args[0:1]...)

	case BCNEZ, BCEQZ:
		args = args[1:2]

	case BEQ, BNE:
		rj := inst.Args[0].(Reg)
		rd := inst.Args[1].(Reg)
		if rj == rd && inst.Op == BEQ {
			op = "JMP"
			args = args[2:]
		} else if rj == R0 {
			args = args[1:]
		} else if rd == R0 {
			args = append(args[:1], args[2:]...)
		}

	case BEQZ, BNEZ:
		if inst.Args[0].(Reg) == R0 && inst.Op == BEQ {
			op = "JMP"
			args = args[1:]
		}

	case BLT, BLTU, BGE, BGEU:
		rj := inst.Args[0].(Reg)
		rd := inst.Args[1].(Reg)
		if rj == rd && (inst.Op == BGE || inst.Op == BGEU) {
			op = "JMP"
			args = args[2:]
		} else if rj == R0 {
			switch inst.Op {
			case BGE:
				op = "BLEZ"
			case BLT:
				op = "BGTZ"
			}
			args = args[1:]
		} else if rd == R0 {
			if !strings.HasSuffix(op, "U") {
				op += "Z"
			}
			args = append(args[:1], args[2:]...)
		}

	case JIRL:
		rd := inst.Args[0].(Reg)
		rj := inst.Args[1].(Reg)
		regno := uint16(rj) & 31
		if rd == R0 {
			return fmt.Sprintf("JMP (R%d)", regno)
		}
		return fmt.Sprintf("CALL (R%d)", regno)

	case LD_B, LD_H, LD_W, LD_D, LD_BU, LD_HU, LD_WU, LL_W, LL_D,
		ST_B, ST_H, ST_W, ST_D, SC_W, SC_D, FLD_S, FLD_D, FST_S, FST_D:
		var off int32
		switch a := inst.Args[2].(type) {
		case Simm16:
			off = signumConvInt32(int32(a.Imm), a.Width)
		case Simm32:
			off = signumConvInt32(int32(a.Imm), a.Width) >> 2
		}
		Iop := strings.ToUpper(inst.Op.String())
		if strings.HasPrefix(Iop, "L") || strings.HasPrefix(Iop, "FL") {
			return fmt.Sprintf("%s %d(%s), %s", op, off, args[1], args[0])
		}
		return fmt.Sprintf("%s %s, %d(%s)", op, args[0], off, args[1])

	case CACOP:
		code := inst.Args[0].(CodeSimm)
		si12 := inst.Args[2].(Simm16)
		off := signumConvInt32(int32(si12.Imm), si12.Width)
		return fmt.Sprintf("%s R%d, %d(%s)", op, code, off, args[1])

	default:
		// Reverse args, placing dest last
		for i, j := 0, len(args)-1; i < j; i, j = i+1, j-1 {
			args[i], args[j] = args[j], args[i]
		}
		switch len(args) {
		case 0, 1:
			if inst.Op != B && inst.Op != BL {
				return op
			}

		case 3:
			switch a0 := inst.Args[0].(type) {
			case Reg:
				rj := inst.Args[1].(Reg)
				if a0 == rj && a0 != R0 {
					args = args[0:2]
				}
			case Fcc:
				args = args[0:2]
			}
			switch inst.Op {
			case SUB_W, SUB_D, ADDI_W, ADDI_D, ORI:
				rj := inst.Args[1].(Reg)
				if rj == R0 {
					args = append(args[0:1], args[2:]...)
					if inst.Op == SUB_W {
						op = "NEGW"
					} else if inst.Op == SUB_D {
						op = "NEGV"
					} else {
						op = "MOVW"
					}
				}

			case ANDI:
				ui12 := inst.Args[2].(Uimm)
				if ui12.Imm == uint32(0xff) {
					op = "MOVBU"
					args = args[1:]
				} else if ui12.Imm == 0 && inst.Args[0].(Reg) == R0 && inst.Args[1].(Reg) == R0 {
					return "NOOP"
				}

			case SLL_W, OR:
				rk := inst.Args[2].(Reg)
				if rk == R0 {
					args = args[1:]
					if inst.Op == SLL_W {
						op = "MOVW"
					} else {
						op = "MOVV"
					}
				}
			}
		}
	}

	if args != nil {
		op += " " + strings.Join(args, ", ")
	}
	return op
}

func plan9Arg(inst *Inst, pc uint64, symname func(uint64) (string, uint64), arg Arg) string {
	// Reg:			gpr[0, 31] and fpr[0, 31]
	// Fcsr:		fcsr[0, 3]
	// Fcc:			fcc[0, 7]
	// Uimm:		unsigned integer constant
	// Simm16:		si16
	// Simm32:		si32
	// OffsetSimm:	si32
	switch a := arg.(type) {
	case Reg:
		regenum := uint16(a)
		regno := uint16(a) & 0x1f
		// General-purpose register
		if regenum >= uint16(R0) && regenum <= uint16(R31) {
			return fmt.Sprintf("R%d", regno)
		} else { // Float point register
			return fmt.Sprintf("F%d", regno)
		}

	case Fcsr:
		regno := uint8(a) & 0x1f
		return fmt.Sprintf("FCSR%d", regno)

	case Fcc:
		regno := uint8(a) & 0x1f
		return fmt.Sprintf("FCC%d", regno)

	case Uimm:
		return fmt.Sprintf("$%d", a.Imm)

	case Simm16:
		si16 := signumConvInt32(int32(a.Imm), a.Width)
		return fmt.Sprintf("$%d", si16)

	case Simm32:
		si32 := signumConvInt32(a.Imm, a.Width)
		return fmt.Sprintf("$%d", si32)

	case OffsetSimm:
		offs := offsConvInt32(a.Imm, a.Width)
		if inst.Op == B || inst.Op == BL {
			addr := int64(pc) + int64(a.Imm)
			if s, base := symname(uint64(addr)); s != "" && uint64(addr) == base {
				return fmt.Sprintf("%s(SB)", s)
			}
		}
		return fmt.Sprintf("%d(PC)", offs>>2)

	case SaSimm:
		return fmt.Sprintf("$%d", a)

	case CodeSimm:
		return fmt.Sprintf("$%d", a)

	}
	return strings.ToUpper(arg.String())
}

func signumConvInt32(imm int32, width uint8) int32 {
	active := uint32(1<<width) - 1
	signum := uint32(imm) & active
	if ((signum >> (width - 1)) & 0x1) == 1 {
		signum |= ^active
	}
	return int32(signum)
}

func offsConvInt32(imm int32, width uint8) int32 {
	relWidth := width + 2
	return signumConvInt32(imm, relWidth)
}

var plan9OpMap = map[Op]string{
	ADD_W:       "ADD",
	ADD_D:       "ADDV",
	SUB_W:       "SUB",
	SUB_D:       "SUBV",
	ADDI_W:      "ADD",
	ADDI_D:      "ADDV",
	LU12I_W:     "LU12IW",
	LU32I_D:     "LU32ID",
	LU52I_D:     "LU52ID",
	SLT:         "SGT",
	SLTU:        "SGTU",
	SLTI:        "SGT",
	SLTUI:       "SGTU",
	PCADDU12I:   "PCADDU12I",
	PCALAU12I:   "PCALAU12I",
	AND:         "AND",
	OR:          "OR",
	NOR:         "NOR",
	XOR:         "XOR",
	ANDI:        "AND",
	ORI:         "OR",
	XORI:        "XOR",
	MUL_W:       "MUL",
	MULH_W:      "MULH",
	MULH_WU:     "MULHU",
	MUL_D:       "MULV",
	MULH_D:      "MULHV",
	MULH_DU:     "MULHVU",
	DIV_W:       "DIV",
	DIV_WU:      "DIVU",
	DIV_D:       "DIVV",
	DIV_DU:      "DIVVU",
	MOD_W:       "REM",
	MOD_WU:      "REMU",
	MOD_D:       "REMV",
	MOD_DU:      "REMVU",
	SLL_W:       "SLL",
	SRL_W:       "SRL",
	SRA_W:       "SRA",
	ROTR_W:      "ROTR",
	SLL_D:       "SLLV",
	SRL_D:       "SRLV",
	SRA_D:       "SRAV",
	ROTR_D:      "ROTRV",
	SLLI_W:      "SLL",
	SRLI_W:      "SRL",
	SRAI_W:      "SRA",
	ROTRI_W:     "ROTR",
	SLLI_D:      "SLLV",
	SRLI_D:      "SRLV",
	SRAI_D:      "SRAV",
	ROTRI_D:     "ROTRV",
	EXT_W_B:     "",
	EXT_W_H:     "",
	CLO_W:       "CLO",
	CLZ_W:       "CLZ",
	BSTRPICK_D:  "",
	MASKEQZ:     "MASKEQZ",
	MASKNEZ:     "MASKNEZ",
	BCNEZ:       "BFPT",
	BCEQZ:       "BFPF",
	BEQ:         "BEQ",
	BNE:         "BNE",
	BEQZ:        "BEQ",
	BNEZ:        "BNE",
	BLT:         "BLT",
	BLTU:        "BLTU",
	BGE:         "BGE",
	BGEU:        "BGEU",
	B:           "JMP",
	BL:          "CALL",
	LD_B:        "MOVB",
	LD_H:        "MOVH",
	LD_W:        "MOVW",
	LD_D:        "MOVV",
	LD_BU:       "MOVBU",
	LD_HU:       "MOVHU",
	LD_WU:       "MOVWU",
	ST_B:        "MOVB",
	ST_H:        "MOVH",
	ST_W:        "MOVW",
	ST_D:        "MOVV",
	AMSWAP_W:    "AMSWAPW",
	AMSWAP_D:    "AMSWAPD",
	AMADD_W:     "AMADDW",
	AMADD_D:     "AMADDV",
	AMAND_W:     "AMANDW",
	AMAND_D:     "AMANDV",
	AMOR_W:      "AMORW",
	AMOR_D:      "AMORV",
	AMXOR_W:     "AMXORW",
	AMXOR_D:     "AMXORV",
	AMMAX_W:     "AMMAXW",
	AMMAX_D:     "AMMAXV",
	AMMIN_W:     "AMMINW",
	AMMIN_D:     "AMMINV",
	AMMAX_WU:    "AMMAXWU",
	AMMAX_DU:    "AMMAXVU",
	AMMIN_WU:    "AMMINWU",
	AMMIN_DU:    "AMMINVU",
	AMSWAP_DB_W: "AMSWAPDBW",
	AMSWAP_DB_D: "AMSWAPDBV",
	AMADD_DB_W:  "AMADDDBW",
	AMADD_DB_D:  "AMADDDBV",
	AMAND_DB_W:  "AMANDDBW",
	AMAND_DB_D:  "AMANDDBV",
	AMOR_DB_W:   "AMORDBW",
	AMOR_DB_D:   "AMORDBV",
	AMXOR_DB_W:  "AMXORDBW",
	AMXOR_DB_D:  "AMXORDBV",
	AMMAX_DB_W:  "AMMAXDBW",
	AMMAX_DB_D:  "AMMAXDBV",
	AMMIN_DB_W:  "AMMINDBW",
	AMMIN_DB_D:  "AMMINDBV",
	AMMAX_DB_WU: "AMMAXDBWU",
	AMMAX_DB_DU: "AMMAXDBVU",
	AMMIN_DB_WU: "AMMINDBWU",
	AMMIN_DB_DU: "AMMINDBVU",
	LL_W:        "LL",
	LL_D:        "LLV",
	SC_W:        "SC",
	SC_D:        "SCV",
	DBAR:        "DBAR",
	SYSCALL:     "SYSCALL",
	BREAK:       "BREAK",
	CACOP:       "BREAK",
	RDTIMEL_W:   "RDTIMELW",
	RDTIMEH_W:   "RDTIMEHW",
	RDTIME_D:    "RDTIMED",
	FADD_S:      "ADDF",
	FADD_D:      "ADDD",
	FSUB_S:      "SUBF",
	FSUB_D:      "SUBD",
	FMUL_S:      "MULF",
	FMUL_D:      "MULD",
	FDIV_S:      "DIVF",
	FDIV_D:      "DIVD",
	FABS_S:      "ABSF",
	FABS_D:      "ABSD",
	FNEG_S:      "NEGF",
	FNEG_D:      "NEGD",
	FSQRT_S:     "SQRTF",
	FSQRT_D:     "SQRTD",
	FCMP_CEQ_S:  "CMPEQF",
	FCMP_CEQ_D:  "CMPEQD",
	FCMP_SLE_S:  "CMPGEF",
	FCMP_SLE_D:  "CMPGED",
	FCMP_SLT_S:  "CMPGTF",
	FCMP_SLT_D:  "CMPGTD",
	FCVT_D_S:    "MOVFD",
	FCVT_S_D:    "MOVDF",
	FFINT_S_W:   "MOVWF",
	FFINT_S_L:   "MOVVF",
	FFINT_D_W:   "MOVWD",
	FFINT_D_L:   "MOVVD",
	FTINT_W_S:   "MOVFW",
	FTINT_L_S:   "MOVFV",
	FTINT_W_D:   "MOVDW",
	FTINT_L_D:   "MOVDV",
	FTINTRZ_W_S: "TRUNCFW",
	FTINTRZ_W_D: "TRUNCDW",
	FTINTRZ_L_S: "TRUNCFV",
	FTINTRZ_L_D: "TRUNCDV",
	FMOV_S:      "MOVF",
	FMOV_D:      "MOVD",
	MOVGR2FR_W:  "MOVW",
	MOVGR2FR_D:  "MOVV",
	MOVFR2GR_S:  "MOVW",
	MOVFR2GR_D:  "MOVV",
	MOVGR2CF:    "MOVV",
	MOVCF2GR:    "MOVV",
	FLD_S:       "MOVF",
	FLD_D:       "MOVD",
	FST_S:       "MOVF",
	FST_D:       "MOVD",
}
