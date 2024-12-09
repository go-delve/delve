package linutil

import (
	"fmt"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
)

// LOONG64Registers is a wrapper for sys.PtraceRegs.
type LOONG64Registers struct {
	Regs       *LOONG64PtraceRegs // general-purpose registers
	iscgo      bool
	tp_tls     uint64
	Fpregs     []proc.Register // Formatted floating point registers
	Fpregset   []byte          // holding all floating point register values
	loadFpRegs func(*LOONG64Registers) error
}

func NewLOONG64Registers(regs *LOONG64PtraceRegs, iscgo bool, tp_tls uint64,
	loadFpRegs func(*LOONG64Registers) error) *LOONG64Registers {
	return &LOONG64Registers{
		Regs:       regs,
		iscgo:      iscgo,
		tp_tls:     tp_tls,
		loadFpRegs: loadFpRegs,
	}
}

// LOONG64PtraceRegs is the struct used by the linux kernel to return the GPR for
// LoongArch64 CPUs. refer to sys/unix/ztype_linux_loong64.go
type LOONG64PtraceRegs struct {
	Regs     [32]uint64
	Orig_a0  uint64
	Era      uint64
	Badv     uint64
	Reserved [10]uint64
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *LOONG64Registers) Slice(floatingPoint bool) ([]proc.Register, error) {
	var regs64 = []struct {
		k string
		v uint64
	}{
		{"R0", r.Regs.Regs[0]},
		{"R1", r.Regs.Regs[1]},
		{"R2", r.Regs.Regs[2]},
		{"R3", r.Regs.Regs[3]},
		{"R4", r.Regs.Regs[4]},
		{"R5", r.Regs.Regs[5]},
		{"R6", r.Regs.Regs[6]},
		{"R7", r.Regs.Regs[7]},
		{"R8", r.Regs.Regs[8]},
		{"R9", r.Regs.Regs[9]},
		{"R10", r.Regs.Regs[10]},
		{"R11", r.Regs.Regs[11]},
		{"R12", r.Regs.Regs[12]},
		{"R13", r.Regs.Regs[13]},
		{"R14", r.Regs.Regs[14]},
		{"R15", r.Regs.Regs[15]},
		{"R16", r.Regs.Regs[16]},
		{"R17", r.Regs.Regs[17]},
		{"R18", r.Regs.Regs[18]},
		{"R19", r.Regs.Regs[19]},
		{"R20", r.Regs.Regs[20]},
		{"R21", r.Regs.Regs[21]},
		{"R22", r.Regs.Regs[22]},
		{"R23", r.Regs.Regs[23]},
		{"R24", r.Regs.Regs[24]},
		{"R25", r.Regs.Regs[25]},
		{"R26", r.Regs.Regs[26]},
		{"R27", r.Regs.Regs[27]},
		{"R28", r.Regs.Regs[28]},
		{"R29", r.Regs.Regs[29]},
		{"R30", r.Regs.Regs[30]},
		{"R31", r.Regs.Regs[31]},
		{"ERA", r.Regs.Era},
		{"BADV", r.Regs.Badv},
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
func (r *LOONG64Registers) PC() uint64 {
	// PC Register
	return r.Regs.Era
}

// SP returns the value of SP register.
func (r *LOONG64Registers) SP() uint64 {
	// Stack pointer
	return r.Regs.Regs[regnum.LOONG64_SP]
}

// BP returns the value of FP register
func (r *LOONG64Registers) BP() uint64 {
	// unused FP register
	return 0
}

// TLS returns the address of the thread local storage memory segment.
func (r *LOONG64Registers) TLS() uint64 {
	if !r.iscgo {
		return 0
	}
	// refer to golang defined REGG: loong64/a.out.go
	return r.tp_tls
}

// GAddr returns the address of the G variable if it is known, 0 and false otherwise.
func (r *LOONG64Registers) GAddr() (uint64, bool) {
	// REGG is $r22,store the address of g
	return r.Regs.Regs[regnum.LOONG64_R22], !r.iscgo
}

// LR returns the link register.
func (r *LOONG64Registers) LR() uint64 {
	return r.Regs.Regs[regnum.LOONG64_LR]
}

// Copy returns a copy of these registers that is guaranteed not to change.
func (r *LOONG64Registers) Copy() (proc.Registers, error) {
	if r.loadFpRegs != nil {
		err := r.loadFpRegs(r)
		r.loadFpRegs = nil
		if err != nil {
			return nil, err
		}
	}

	var rr LOONG64Registers
	rr.Regs = &LOONG64PtraceRegs{}
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

func (r *LOONG64Registers) SetReg(regNum uint64, reg *op.DwarfRegister) (fpchanged bool, err error) {
	switch regNum {
	case regnum.LOONG64_SP:
		r.Regs.Regs[regnum.LOONG64_SP] = reg.Uint64Val
		return false, nil

	case regnum.LOONG64_PC:
		r.Regs.Era = reg.Uint64Val
		return false, nil
	}

	switch {
	case regNum >= regnum.LOONG64_R0 && regNum <= regnum.LOONG64_R31:
		r.Regs.Regs[regNum] = reg.Uint64Val
		return false, nil

	case regNum >= regnum.LOONG64_F0 && regNum <= regnum.LOONG64_F31:
		if r.loadFpRegs != nil {
			err := r.loadFpRegs(r)
			r.loadFpRegs = nil
			if err != nil {
				return false, err
			}
		}

		i := regNum - regnum.LOONG64_F0
		reg.FillBytes()
		copy(r.Fpregset[8*i:], reg.Bytes)
		return true, nil

	default:
		return false, fmt.Errorf("changing register %d not implemented", regNum)
	}
}

// LOONG64PtraceFpRegs refers to the definition of struct user_fp_state in kernel ptrace.h
type LOONG64PtraceFpRegs struct {
	Fregs []byte
	Fcc   uint64
	Fcsr  uint32
}

const _LOONG64_FPREGSET_LENGTH = (32 * 8)

func (fpregs *LOONG64PtraceFpRegs) Decode() (regs []proc.Register) {
	for i := 0; i < len(fpregs.Fregs); i += 8 {
		name := fmt.Sprintf("F%d", (i / 8))
		value := fpregs.Fregs[i : i+8]
		regs = proc.AppendBytesRegister(regs, name, value)
	}

	regs = proc.AppendUint64Register(regs, "FCC0", fpregs.Fcc)
	regs = proc.AppendUint64Register(regs, "FCSR", uint64(fpregs.Fcsr))

	return
}

func (fpregs *LOONG64PtraceFpRegs) Byte() []byte {
	fpregs.Fregs = make([]byte, _LOONG64_FPREGSET_LENGTH)

	return fpregs.Fregs[:]
}
