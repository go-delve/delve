package fbsdutil

import (
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
