package linutil

import (
	"encoding/binary"
	"fmt"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
	"golang.org/x/arch/riscv64/riscv64asm"
)

// RISCV64Registers implements the proc.Registers interface.
type RISCV64Registers struct {
	Regs       *RISCV64PtraceRegs // general-purpose registers & pc
	iscgo      bool
	tp_tls     uint64
	Fpregs     []proc.Register // Formatted floating-point registers
	Fpregset   []byte          // holding all floating-point register values
	loadFpRegs func(*RISCV64Registers) error
}

func NewRISCV64Registers(regs *RISCV64PtraceRegs, iscgo bool, tp_tls uint64,
	loadFpRegs func(*RISCV64Registers) error) *RISCV64Registers {
	return &RISCV64Registers{
		Regs:       regs,
		iscgo:      iscgo,
		tp_tls:     tp_tls,
		loadFpRegs: loadFpRegs,
	}
}

// RISCV64PtraceRegs is the struct used by the linux kernel to return the
// general purpose registers for RISC-V CPUs.
type RISCV64PtraceRegs struct {
	Pc  uint64
	Ra  uint64
	Sp  uint64
	Gp  uint64
	Tp  uint64
	T0  uint64
	T1  uint64
	T2  uint64
	S0  uint64
	S1  uint64
	A0  uint64
	A1  uint64
	A2  uint64
	A3  uint64
	A4  uint64
	A5  uint64
	A6  uint64
	A7  uint64
	S2  uint64
	S3  uint64
	S4  uint64
	S5  uint64
	S6  uint64
	S7  uint64
	S8  uint64
	S9  uint64
	S10 uint64
	S11 uint64
	T3  uint64
	T4  uint64
	T5  uint64
	T6  uint64
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *RISCV64Registers) Slice(floatingPoint bool) ([]proc.Register, error) {
	var regs64 = []struct {
		k string
		v uint64
	}{
		{"X1", r.Regs.Ra},
		{"X2", r.Regs.Sp},
		{"X3", r.Regs.Gp},
		{"X4", r.Regs.Tp},
		{"X5", r.Regs.T0},
		{"X6", r.Regs.T1},
		{"X7", r.Regs.T2},
		{"X8", r.Regs.S0},
		{"X9", r.Regs.S1},
		{"X10", r.Regs.A0},
		{"X11", r.Regs.A1},
		{"X12", r.Regs.A2},
		{"X13", r.Regs.A3},
		{"X14", r.Regs.A4},
		{"X15", r.Regs.A5},
		{"X16", r.Regs.A6},
		{"X17", r.Regs.A7},
		{"X18", r.Regs.S2},
		{"X19", r.Regs.S3},
		{"X20", r.Regs.S4},
		{"X21", r.Regs.S5},
		{"X22", r.Regs.S6},
		{"X23", r.Regs.S7},
		{"X24", r.Regs.S8},
		{"X25", r.Regs.S9},
		{"X26", r.Regs.S10},
		{"X27", r.Regs.S11},
		{"X28", r.Regs.T3},
		{"X29", r.Regs.T4},
		{"X30", r.Regs.T5},
		{"X31", r.Regs.T6},
		{"PC", r.Regs.Pc},
	}

	out := make([]proc.Register, 0, (len(regs64) + len(r.Fpregs)))
	for _, reg := range regs64 {
		out = proc.AppendUint64Register(out, reg.k, reg.v)
	}

	var floatLoadError error
	if floatingPoint {
		if r.loadFpRegs != nil {
			floatLoadError = r.loadFpRegs(r)
			r.loadFpRegs = nil
		}

		out = append(out, r.Fpregs...)
	}

	return out, floatLoadError
}

// PC returns the value of PC register.
func (r *RISCV64Registers) PC() uint64 {
	// PC Register
	return r.Regs.Pc
}

// SP returns the value of SP register.
func (r *RISCV64Registers) SP() uint64 {
	// Stack pointer
	return r.Regs.Sp
}

// BP returns the value of FP register
func (r *RISCV64Registers) BP() uint64 {
	// unused FP register
	return 0
}

// TLS returns the address of the thread local storage memory segment.
func (r *RISCV64Registers) TLS() uint64 {
	// TODO: calling cgo may overwrite $x27, read it from the kernel
	if !r.iscgo {
		return 0
	}

	return r.tp_tls
}

// GAddr returns the address of the G variable if it is known, 0 and false otherwise.
func (r *RISCV64Registers) GAddr() (uint64, bool) {
	// Defined in $GOROOT/cmd/internal/obj/riscv/cpu.go.
	return r.Regs.S11, !r.iscgo
}

// LR returns the link register.
func (r *RISCV64Registers) LR() uint64 {
	return r.Regs.Ra
}

// Copy returns a copy of these registers that is guaranteed not to change.
func (r *RISCV64Registers) Copy() (proc.Registers, error) {
	if r.loadFpRegs != nil {
		err := r.loadFpRegs(r)
		r.loadFpRegs = nil
		if err != nil {
			return nil, err
		}
	}

	var rr RISCV64Registers
	rr.Regs = &RISCV64PtraceRegs{}
	*(rr.Regs) = *(r.Regs)
	if r.Fpregs != nil {
		rr.Fpregs = make([]proc.Register, len(r.Fpregs))
		copy(rr.Fpregs, r.Fpregs)
	}

	if r.Fpregset != nil {
		rr.Fpregset = make([]byte, len(r.Fpregset))
		copy(rr.Fpregset, r.Fpregset)
	}

	return &rr, nil
}

func (r *RISCV64Registers) GetReg(regNum uint64) (uint64, error) {
	reg := riscv64asm.Reg(regNum)

	if reg <= riscv64asm.X31 {
		switch regNum {
		case regnum.RISCV64_LR:
			return uint64(r.Regs.Ra), nil
		case regnum.RISCV64_SP:
			return uint64(r.Regs.Sp), nil
		case regnum.RISCV64_GP:
			return uint64(r.Regs.Gp), nil
		case regnum.RISCV64_TP:
			return uint64(r.Regs.Tp), nil
		case regnum.RISCV64_T0:
			return uint64(r.Regs.T0), nil
		case regnum.RISCV64_T1:
			return uint64(r.Regs.T1), nil
		case regnum.RISCV64_T2:
			return uint64(r.Regs.T2), nil
		case regnum.RISCV64_S0:
			return uint64(r.Regs.S0), nil
		case regnum.RISCV64_S1:
			return uint64(r.Regs.S1), nil
		case regnum.RISCV64_A0:
			return uint64(r.Regs.A0), nil
		case regnum.RISCV64_A1:
			return uint64(r.Regs.A1), nil
		case regnum.RISCV64_A2:
			return uint64(r.Regs.A2), nil
		case regnum.RISCV64_A3:
			return uint64(r.Regs.A3), nil
		case regnum.RISCV64_A4:
			return uint64(r.Regs.A4), nil
		case regnum.RISCV64_A5:
			return uint64(r.Regs.A5), nil
		case regnum.RISCV64_A6:
			return uint64(r.Regs.A6), nil
		case regnum.RISCV64_A7:
			return uint64(r.Regs.A7), nil
		case regnum.RISCV64_S2:
			return uint64(r.Regs.S2), nil
		case regnum.RISCV64_S3:
			return uint64(r.Regs.S3), nil
		case regnum.RISCV64_S4:
			return uint64(r.Regs.S4), nil
		case regnum.RISCV64_S5:
			return uint64(r.Regs.S5), nil
		case regnum.RISCV64_S6:
			return uint64(r.Regs.S6), nil
		case regnum.RISCV64_S7:
			return uint64(r.Regs.S7), nil
		case regnum.RISCV64_S8:
			return uint64(r.Regs.S8), nil
		case regnum.RISCV64_S9:
			return uint64(r.Regs.S9), nil
		case regnum.RISCV64_S10:
			return uint64(r.Regs.S10), nil
		case regnum.RISCV64_S11:
			return uint64(r.Regs.S11), nil
		case regnum.RISCV64_T3:
			return uint64(r.Regs.T3), nil
		case regnum.RISCV64_T4:
			return uint64(r.Regs.T4), nil
		case regnum.RISCV64_T5:
			return uint64(r.Regs.T5), nil
		case regnum.RISCV64_T6:
			return uint64(r.Regs.T6), nil
		}
	}

	return 0, proc.ErrUnknownRegister
}

func (r *RISCV64Registers) SetReg(regNum uint64, reg *op.DwarfRegister) (fpchanged bool, err error) {
	var p *uint64
	switch regNum {
	case regnum.RISCV64_LR:
		p = &r.Regs.Ra
	case regnum.RISCV64_SP:
		p = &r.Regs.Sp
	case regnum.RISCV64_GP:
		p = &r.Regs.Gp
	case regnum.RISCV64_TP:
		p = &r.Regs.Tp
	case regnum.RISCV64_T0:
		p = &r.Regs.T0
	case regnum.RISCV64_T1:
		p = &r.Regs.T1
	case regnum.RISCV64_T2:
		p = &r.Regs.T2
	case regnum.RISCV64_S0:
		p = &r.Regs.S0
	case regnum.RISCV64_S1:
		p = &r.Regs.S1
	case regnum.RISCV64_A0:
		p = &r.Regs.A0
	case regnum.RISCV64_A1:
		p = &r.Regs.A1
	case regnum.RISCV64_A2:
		p = &r.Regs.A2
	case regnum.RISCV64_A3:
		p = &r.Regs.A3
	case regnum.RISCV64_A4:
		p = &r.Regs.A4
	case regnum.RISCV64_A5:
		p = &r.Regs.A5
	case regnum.RISCV64_A6:
		p = &r.Regs.A6
	case regnum.RISCV64_A7:
		p = &r.Regs.A7
	case regnum.RISCV64_S2:
		p = &r.Regs.S2
	case regnum.RISCV64_S3:
		p = &r.Regs.S3
	case regnum.RISCV64_S4:
		p = &r.Regs.S4
	case regnum.RISCV64_S5:
		p = &r.Regs.S5
	case regnum.RISCV64_S6:
		p = &r.Regs.S6
	case regnum.RISCV64_S7:
		p = &r.Regs.S7
	case regnum.RISCV64_S8:
		p = &r.Regs.S8
	case regnum.RISCV64_S9:
		p = &r.Regs.S9
	case regnum.RISCV64_S10:
		p = &r.Regs.S10
	case regnum.RISCV64_S11:
		p = &r.Regs.S11
	case regnum.RISCV64_T3:
		p = &r.Regs.T3
	case regnum.RISCV64_T4:
		p = &r.Regs.T4
	case regnum.RISCV64_T5:
		p = &r.Regs.T5
	case regnum.RISCV64_T6:
		p = &r.Regs.T6
	case regnum.RISCV64_PC:
		p = &r.Regs.Pc
	}

	if p != nil {
		*p = reg.Uint64Val
		return false, nil
	}

	switch {
	case regNum >= regnum.RISCV64_F0 && regNum <= regnum.RISCV64_F31:
		if r.loadFpRegs != nil {
			err := r.loadFpRegs(r)
			r.loadFpRegs = nil
			if err != nil {
				return false, err
			}
		}

		i := regNum - regnum.RISCV64_F0
		reg.FillBytes()
		copy(r.Fpregset[8*i:], reg.Bytes)
		return true, nil

	default:
		return false, fmt.Errorf("changing register %d not implemented", regNum)
	}
}

// RISCV64PtraceFpRegs is refer to the definition of struct __riscv_d_ext_state in the kernel ptrace.h
type RISCV64PtraceFpRegs struct {
	Fregs []byte
	Fcsr  uint32
}

const _RISCV64_FPREGSET_LENGTH = (32 * 8)

func (fpregs *RISCV64PtraceFpRegs) Decode() (regs []proc.Register) {
	for i := 0; i < len(fpregs.Fregs); i += 8 {
		name := fmt.Sprintf("F%d", (i / 8))
		value := fpregs.Fregs[i : i+8]
		regs = proc.AppendBytesRegister(regs, name, value)
	}

	fcsrBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(fcsrBytes, uint32(fpregs.Fcsr))
	regs = proc.AppendBytesRegister(regs, "FCSR", fcsrBytes)

	return
}

func (fpregs *RISCV64PtraceFpRegs) Byte() []byte {
	fpregs.Fregs = make([]byte, _RISCV64_FPREGSET_LENGTH)

	return fpregs.Fregs[:]
}
