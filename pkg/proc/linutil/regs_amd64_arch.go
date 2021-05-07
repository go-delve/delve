package linutil

import (
	"fmt"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
)

// AMD64Registers implements the proc.Registers interface for the native/linux
// backend and core/linux backends, on AMD64.
type AMD64Registers struct {
	Regs     *AMD64PtraceRegs
	Fpregs   []proc.Register
	Fpregset *amd64util.AMD64Xstate

	loadFpRegs func(*AMD64Registers) error
}

func NewAMD64Registers(regs *AMD64PtraceRegs, loadFpRegs func(*AMD64Registers) error) *AMD64Registers {
	return &AMD64Registers{Regs: regs, loadFpRegs: loadFpRegs}
}

// AMD64PtraceRegs is the struct used by the linux kernel to return the
// general purpose registers for AMD64 CPUs.
type AMD64PtraceRegs struct {
	R15      uint64
	R14      uint64
	R13      uint64
	R12      uint64
	Rbp      uint64
	Rbx      uint64
	R11      uint64
	R10      uint64
	R9       uint64
	R8       uint64
	Rax      uint64
	Rcx      uint64
	Rdx      uint64
	Rsi      uint64
	Rdi      uint64
	Orig_rax uint64
	Rip      uint64
	Cs       uint64
	Eflags   uint64
	Rsp      uint64
	Ss       uint64
	Fs_base  uint64
	Gs_base  uint64
	Ds       uint64
	Es       uint64
	Fs       uint64
	Gs       uint64
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *AMD64Registers) Slice(floatingPoint bool) ([]proc.Register, error) {
	var regs = []struct {
		k string
		v uint64
	}{
		{"Rip", r.Regs.Rip},
		{"Rsp", r.Regs.Rsp},
		{"Rax", r.Regs.Rax},
		{"Rbx", r.Regs.Rbx},
		{"Rcx", r.Regs.Rcx},
		{"Rdx", r.Regs.Rdx},
		{"Rdi", r.Regs.Rdi},
		{"Rsi", r.Regs.Rsi},
		{"Rbp", r.Regs.Rbp},
		{"R8", r.Regs.R8},
		{"R9", r.Regs.R9},
		{"R10", r.Regs.R10},
		{"R11", r.Regs.R11},
		{"R12", r.Regs.R12},
		{"R13", r.Regs.R13},
		{"R14", r.Regs.R14},
		{"R15", r.Regs.R15},
		{"Orig_rax", r.Regs.Orig_rax},
		{"Cs", r.Regs.Cs},
		{"Rflags", r.Regs.Eflags},
		{"Ss", r.Regs.Ss},
		{"Fs_base", r.Regs.Fs_base},
		{"Gs_base", r.Regs.Gs_base},
		{"Ds", r.Regs.Ds},
		{"Es", r.Regs.Es},
		{"Fs", r.Regs.Fs},
		{"Gs", r.Regs.Gs},
	}
	out := make([]proc.Register, 0, len(regs)+len(r.Fpregs))
	for _, reg := range regs {
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
func (r *AMD64Registers) PC() uint64 {
	return r.Regs.Rip
}

// SP returns the value of RSP register.
func (r *AMD64Registers) SP() uint64 {
	return r.Regs.Rsp
}

func (r *AMD64Registers) BP() uint64 {
	return r.Regs.Rbp
}

// TLS returns the address of the thread local storage memory segment.
func (r *AMD64Registers) TLS() uint64 {
	return r.Regs.Fs_base
}

// GAddr returns the address of the G variable if it is known, 0 and false
// otherwise.
func (r *AMD64Registers) GAddr() (uint64, bool) {
	return 0, false
}

// Copy returns a copy of these registers that is guaranteed not to change.
func (r *AMD64Registers) Copy() (proc.Registers, error) {
	if r.loadFpRegs != nil {
		err := r.loadFpRegs(r)
		r.loadFpRegs = nil
		if err != nil {
			return nil, err
		}
	}
	var rr AMD64Registers
	rr.Regs = &AMD64PtraceRegs{}
	rr.Fpregset = &amd64util.AMD64Xstate{}
	*(rr.Regs) = *(r.Regs)
	if r.Fpregset != nil {
		*(rr.Fpregset) = *(r.Fpregset)
	}
	if r.Fpregs != nil {
		rr.Fpregs = make([]proc.Register, len(r.Fpregs))
		copy(rr.Fpregs, r.Fpregs)
	}
	return &rr, nil
}

func (r *AMD64Registers) SetReg(regNum uint64, reg *op.DwarfRegister) (bool, error) {
	var p *uint64
	switch regNum {
	case regnum.AMD64_Rax:
		p = &r.Regs.Rax
	case regnum.AMD64_Rbx:
		p = &r.Regs.Rbx
	case regnum.AMD64_Rcx:
		p = &r.Regs.Rcx
	case regnum.AMD64_Rdx:
		p = &r.Regs.Rdx
	case regnum.AMD64_Rsi:
		p = &r.Regs.Rsi
	case regnum.AMD64_Rdi:
		p = &r.Regs.Rdi
	case regnum.AMD64_Rbp:
		p = &r.Regs.Rbp
	case regnum.AMD64_Rsp:
		p = &r.Regs.Rsp
	case regnum.AMD64_R8:
		p = &r.Regs.R8
	case regnum.AMD64_R9:
		p = &r.Regs.R9
	case regnum.AMD64_R10:
		p = &r.Regs.R10
	case regnum.AMD64_R11:
		p = &r.Regs.R11
	case regnum.AMD64_R12:
		p = &r.Regs.R12
	case regnum.AMD64_R13:
		p = &r.Regs.R13
	case regnum.AMD64_R14:
		p = &r.Regs.R14
	case regnum.AMD64_R15:
		p = &r.Regs.R15
	case regnum.AMD64_Rip:
		p = &r.Regs.Rip
	}

	if p != nil {
		if reg.Bytes != nil && len(reg.Bytes) != 8 {
			return false, fmt.Errorf("wrong number of bytes for register %s (%d)", regnum.AMD64ToName(regNum), len(reg.Bytes))
		}
		*p = reg.Uint64Val
		return false, nil
	}

	if r.loadFpRegs != nil {
		err := r.loadFpRegs(r)
		if err != nil {
			return false, err
		}
		r.loadFpRegs = nil
	}

	if regNum < regnum.AMD64_XMM0 || regNum > regnum.AMD64_XMM0+15 {
		return false, fmt.Errorf("can not set %s", regnum.AMD64ToName(regNum))
	}

	reg.FillBytes()

	err := r.Fpregset.SetXmmRegister(int(regNum-regnum.AMD64_XMM0), reg.Bytes)
	if err != nil {
		return false, err
	}
	return true, nil
}
