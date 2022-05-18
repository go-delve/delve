package linutil

import (
	"fmt"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
)

// ARM64Registers is a wrapper for sys.PtraceRegs.
type ARM64Registers struct {
	Regs      *ARM64PtraceRegs //general-purpose registers
	iscgo     bool
	tpidr_el0 uint64
	Fpregs    []proc.Register //Formatted floating point registers
	Fpregset  []byte          //holding all floating point register values

	loadFpRegs func(*ARM64Registers) error
}

func NewARM64Registers(regs *ARM64PtraceRegs, iscgo bool, tpidr_el0 uint64, loadFpRegs func(*ARM64Registers) error) *ARM64Registers {
	return &ARM64Registers{Regs: regs, iscgo: iscgo, tpidr_el0: tpidr_el0, loadFpRegs: loadFpRegs}
}

// ARM64PtraceRegs is the struct used by the linux kernel to return the
// general purpose registers for ARM64 CPUs.
// copy from sys/unix/ztypes_linux_arm64.go:735
type ARM64PtraceRegs struct {
	Regs   [31]uint64
	Sp     uint64
	Pc     uint64
	Pstate uint64
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *ARM64Registers) Slice(floatingPoint bool) ([]proc.Register, error) {
	var regs64 = []struct {
		k string
		v uint64
	}{
		{"X0", r.Regs.Regs[0]},
		{"X1", r.Regs.Regs[1]},
		{"X2", r.Regs.Regs[2]},
		{"X3", r.Regs.Regs[3]},
		{"X4", r.Regs.Regs[4]},
		{"X5", r.Regs.Regs[5]},
		{"X6", r.Regs.Regs[6]},
		{"X7", r.Regs.Regs[7]},
		{"X8", r.Regs.Regs[8]},
		{"X9", r.Regs.Regs[9]},
		{"X10", r.Regs.Regs[10]},
		{"X11", r.Regs.Regs[11]},
		{"X12", r.Regs.Regs[12]},
		{"X13", r.Regs.Regs[13]},
		{"X14", r.Regs.Regs[14]},
		{"X15", r.Regs.Regs[15]},
		{"X16", r.Regs.Regs[16]},
		{"X17", r.Regs.Regs[17]},
		{"X18", r.Regs.Regs[18]},
		{"X19", r.Regs.Regs[19]},
		{"X20", r.Regs.Regs[20]},
		{"X21", r.Regs.Regs[21]},
		{"X22", r.Regs.Regs[22]},
		{"X23", r.Regs.Regs[23]},
		{"X24", r.Regs.Regs[24]},
		{"X25", r.Regs.Regs[25]},
		{"X26", r.Regs.Regs[26]},
		{"X27", r.Regs.Regs[27]},
		{"X28", r.Regs.Regs[28]},
		{"X29", r.Regs.Regs[29]},
		{"X30", r.Regs.Regs[30]},
		{"SP", r.Regs.Sp},
		{"PC", r.Regs.Pc},
		{"PSTATE", r.Regs.Pstate},
	}
	out := make([]proc.Register, 0, len(regs64)+len(r.Fpregs))
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

// PC returns the value of RIP register.
func (r *ARM64Registers) PC() uint64 {
	return r.Regs.Pc
}

// SP returns the value of RSP register.
func (r *ARM64Registers) SP() uint64 {
	return r.Regs.Sp
}

func (r *ARM64Registers) BP() uint64 {
	return r.Regs.Regs[29]
}

// TLS returns the address of the thread local storage memory segment.
func (r *ARM64Registers) TLS() uint64 {
	if !r.iscgo {
		return 0
	}
	return r.tpidr_el0
}

// GAddr returns the address of the G variable if it is known, 0 and false
// otherwise.
func (r *ARM64Registers) GAddr() (uint64, bool) {
	return r.Regs.Regs[28], !r.iscgo
}

// LR returns the link register.
func (r *ARM64Registers) LR() uint64 {
	return r.Regs.Regs[30]
}

// Copy returns a copy of these registers that is guaranteed not to change.
func (r *ARM64Registers) Copy() (proc.Registers, error) {
	if r.loadFpRegs != nil {
		err := r.loadFpRegs(r)
		r.loadFpRegs = nil
		if err != nil {
			return nil, err
		}
	}
	var rr ARM64Registers
	rr.Regs = &ARM64PtraceRegs{}
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

func (r *ARM64Registers) SetReg(regNum uint64, reg *op.DwarfRegister) (fpchanged bool, err error) {
	switch regNum {
	case regnum.ARM64_PC:
		r.Regs.Pc = reg.Uint64Val
		return false, nil
	case regnum.ARM64_SP:
		r.Regs.Sp = reg.Uint64Val
		return false, nil
	default:
		switch {
		case regNum >= regnum.ARM64_X0 && regNum <= regnum.ARM64_X0+30:
			r.Regs.Regs[regNum-regnum.ARM64_X0] = reg.Uint64Val
			return false, nil

		case regNum >= regnum.ARM64_V0 && regNum <= regnum.ARM64_V0+30:
			if r.loadFpRegs != nil {
				err := r.loadFpRegs(r)
				r.loadFpRegs = nil
				if err != nil {
					return false, err
				}
			}

			i := regNum - regnum.ARM64_V0
			reg.FillBytes()
			copy(r.Fpregset[16*i:], reg.Bytes)
			return true, nil

		default:
			return false, fmt.Errorf("changing register %d not implemented", regNum)
		}
	}
}

type ARM64PtraceFpRegs struct {
	Vregs []byte
	Fpsr  uint32
	Fpcr  uint32
}

const _ARM_FP_REGS_LENGTH = 512

func (fpregs *ARM64PtraceFpRegs) Decode() (regs []proc.Register) {
	for i := 0; i < len(fpregs.Vregs); i += 16 {
		regs = proc.AppendBytesRegister(regs, fmt.Sprintf("V%d", i/16), fpregs.Vregs[i:i+16])
	}
	return
}

func (fpregs *ARM64PtraceFpRegs) Byte() []byte {
	fpregs.Vregs = make([]byte, _ARM_FP_REGS_LENGTH)
	return fpregs.Vregs[:]
}
