package linutil

import (
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
)

// I386Registers implements the proc.Registers interface for the native/linux
// backend and core/linux backends, on I386.
type I386Registers struct {
	Regs     *I386PtraceRegs
	Fpregs   []proc.Register
	Fpregset *amd64util.AMD64Xstate
	Tls      uint64

	loadFpRegs func(*I386Registers) error
}

func NewI386Registers(regs *I386PtraceRegs, loadFpRegs func(*I386Registers) error) *I386Registers {
	return &I386Registers{Regs: regs, Fpregs: nil, Fpregset: nil, Tls: 0, loadFpRegs: loadFpRegs}
}

// I386PtraceRegs is the struct used by the linux kernel to return the
// general purpose registers for I386 CPUs.
type I386PtraceRegs struct {
	Ebx      int32
	Ecx      int32
	Edx      int32
	Esi      int32
	Edi      int32
	Ebp      int32
	Eax      int32
	Xds      int32
	Xes      int32
	Xfs      int32
	Xgs      int32
	Orig_eax int32
	Eip      int32
	Xcs      int32
	Eflags   int32
	Esp      int32
	Xss      int32
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *I386Registers) Slice(floatingPoint bool) ([]proc.Register, error) {
	var regs = []struct {
		k string
		v int32
	}{
		{"Ebx", r.Regs.Ebx},
		{"Ecx", r.Regs.Ecx},
		{"Edx", r.Regs.Edx},
		{"Esi", r.Regs.Esi},
		{"Edi", r.Regs.Edi},
		{"Ebp", r.Regs.Ebp},
		{"Eax", r.Regs.Eax},
		{"Xds", r.Regs.Xds},
		{"Xes", r.Regs.Xes},
		{"Xfs", r.Regs.Xfs},
		{"Xgs", r.Regs.Xgs},
		{"Orig_eax", r.Regs.Orig_eax},
		{"Eip", r.Regs.Eip},
		{"Xcs", r.Regs.Xcs},
		{"Eflags", r.Regs.Eflags},
		{"Esp", r.Regs.Esp},
		{"Xss", r.Regs.Xss},
	}
	out := make([]proc.Register, 0, len(regs)+len(r.Fpregs))
	for _, reg := range regs {
		out = proc.AppendUint64Register(out, reg.k, uint64(uint32(reg.v)))
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

// PC returns the value of EIP register.
func (r *I386Registers) PC() uint64 {
	return uint64(uint32(r.Regs.Eip))
}

// SP returns the value of ESP register.
func (r *I386Registers) SP() uint64 {
	return uint64(uint32(r.Regs.Esp))
}

func (r *I386Registers) BP() uint64 {
	return uint64(uint32(r.Regs.Ebp))
}

// CX returns the value of ECX register.
func (r *I386Registers) CX() uint64 {
	return uint64(uint32(r.Regs.Ecx))
}

// TLS returns the address of the thread local storage memory segment.
func (r I386Registers) TLS() uint64 {
	return r.Tls
}

// GAddr returns the address of the G variable if it is known, 0 and false
// otherwise.
func (r *I386Registers) GAddr() (uint64, bool) {
	return 0, false
}

// Copy returns a copy of these registers that is guaranteed not to change.
func (r *I386Registers) Copy() (proc.Registers, error) {
	if r.loadFpRegs != nil {
		err := r.loadFpRegs(r)
		r.loadFpRegs = nil
		if err != nil {
			return nil, err
		}
	}
	var rr I386Registers
	rr.Regs = &I386PtraceRegs{}
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
