package fbsdutil

import (
	"fmt"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
)

// AMD64Registers implements the proc.Registers interface for the native/freebsd
// backend and core/freebsd backends, on AMD64.
type AMD64Registers struct {
	Regs     *AMD64PtraceRegs
	Fpregs   []proc.Register
	Fpregset *amd64util.AMD64Xstate
	Fsbase   uint64

	loadFpRegs func(*AMD64Registers) error
}

func NewAMD64Registers(regs *AMD64PtraceRegs, fsbase uint64, loadFpRegs func(*AMD64Registers) error) *AMD64Registers {
	return &AMD64Registers{Regs: regs, Fsbase: fsbase, loadFpRegs: loadFpRegs}
}

// AMD64PtraceRegs is the struct used by the freebsd kernel to return the
// general purpose registers for AMD64 CPUs.
// source: sys/x86/include/reg.h
type AMD64PtraceRegs struct {
	R15    int64
	R14    int64
	R13    int64
	R12    int64
	R11    int64
	R10    int64
	R9     int64
	R8     int64
	Rdi    int64
	Rsi    int64
	Rbp    int64
	Rbx    int64
	Rdx    int64
	Rcx    int64
	Rax    int64
	Trapno uint32
	Fs     uint16
	Gs     uint16
	Err    uint32
	Es     uint16
	Ds     uint16
	Rip    int64
	Cs     int64
	Rflags int64
	Rsp    int64
	Ss     int64
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *AMD64Registers) Slice(floatingPoint bool) ([]proc.Register, error) {
	var regs64 = []struct {
		k string
		v int64
	}{
		{"R15", r.Regs.R15},
		{"R14", r.Regs.R14},
		{"R13", r.Regs.R13},
		{"R12", r.Regs.R12},
		{"R11", r.Regs.R11},
		{"R10", r.Regs.R10},
		{"R9", r.Regs.R9},
		{"R8", r.Regs.R8},
		{"Rdi", r.Regs.Rdi},
		{"Rsi", r.Regs.Rsi},
		{"Rbp", r.Regs.Rbp},
		{"Rbx", r.Regs.Rbx},
		{"Rdx", r.Regs.Rdx},
		{"Rcx", r.Regs.Rcx},
		{"Rax", r.Regs.Rax},
		{"Rip", r.Regs.Rip},
		{"Cs", r.Regs.Cs},
		{"Rflags", r.Regs.Rflags},
		{"Rsp", r.Regs.Rsp},
		{"Ss", r.Regs.Ss},
	}
	var regs32 = []struct {
		k string
		v uint32
	}{
		{"Trapno", r.Regs.Trapno},
		{"Err", r.Regs.Err},
	}
	var regs16 = []struct {
		k string
		v uint16
	}{
		{"Fs", r.Regs.Fs},
		{"Gs", r.Regs.Gs},
		{"Es", r.Regs.Es},
		{"Ds", r.Regs.Ds},
	}
	out := make([]proc.Register, 0,
		len(regs64)+
			len(regs32)+
			len(regs16)+
			1+ // for Rflags
			len(r.Fpregs))
	for _, reg := range regs64 {
		// FreeBSD defines the registers as signed, but Linux defines
		// them as unsigned.  Of course, a register doesn't really have
		// a concept of signedness.  Cast to what Delve expects.
		out = proc.AppendUint64Register(out, reg.k, uint64(reg.v))
	}
	for _, reg := range regs32 {
		out = proc.AppendUint64Register(out, reg.k, uint64(reg.v))
	}
	for _, reg := range regs16 {
		out = proc.AppendUint64Register(out, reg.k, uint64(reg.v))
	}
	// x86 called this register "Eflags".  amd64 extended it and renamed it
	// "Rflags", but Linux still uses the old name.
	out = proc.AppendUint64Register(out, "Rflags", uint64(r.Regs.Rflags))
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
	return uint64(r.Regs.Rip)
}

// SP returns the value of RSP register.
func (r *AMD64Registers) SP() uint64 {
	return uint64(r.Regs.Rsp)
}

func (r *AMD64Registers) BP() uint64 {
	return uint64(r.Regs.Rbp)
}

func (r *AMD64Registers) LR() uint64 {
	return 0
}

// TLS returns the address of the thread local storage memory segment.
func (r *AMD64Registers) TLS() uint64 {
	return r.Fsbase
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
	var p *int64
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
	case regnum.AMD64_Rflags:
		p = &r.Regs.Rflags
	}

	if p != nil {
		if reg.Bytes != nil && len(reg.Bytes) != 8 {
			return false, fmt.Errorf("wrong number of bytes for register %s (%d)", regnum.AMD64ToName(regNum), len(reg.Bytes))
		}
		*p = int64(reg.Uint64Val)
		return false, nil
	}

	if r.loadFpRegs != nil {
		if err := r.loadFpRegs(r); err != nil {
			return false, err
		}
		r.loadFpRegs = nil
	}

	if regNum < regnum.AMD64_XMM0 || regNum > regnum.AMD64_XMM0+15 {
		return false, fmt.Errorf("can not set %s", regnum.AMD64ToName(regNum))
	}

	reg.FillBytes()

	if err := r.Fpregset.SetXmmRegister(int(regNum-regnum.AMD64_XMM0), reg.Bytes); err != nil {
		return false, err
	}
	return true, nil
}
