package fbsdutil

import (
	"golang.org/x/arch/x86/x86asm"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

// AMD64Registers implements the proc.Registers interface for the native/freebsd
// backend and core/freebsd backends, on AMD64.
type AMD64Registers struct {
	Regs     *AMD64PtraceRegs
	Fpregs   []proc.Register
	Fpregset *AMD64Xstate
	Fsbase   uint64
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
func (r *AMD64Registers) Slice(floatingPoint bool) []proc.Register {
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
		out = proc.AppendQwordReg(out, reg.k, uint64(reg.v))
	}
	for _, reg := range regs32 {
		out = proc.AppendDwordReg(out, reg.k, reg.v)
	}
	for _, reg := range regs16 {
		out = proc.AppendWordReg(out, reg.k, reg.v)
	}
	// x86 called this register "Eflags".  amd64 extended it and renamed it
	// "Rflags", but Linux still uses the old name.
	out = proc.AppendEflagReg(out, "Rflags", uint64(r.Regs.Rflags))
	if floatingPoint {
		out = append(out, r.Fpregs...)
	}
	return out
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

// CX returns the value of RCX register.
func (r *AMD64Registers) CX() uint64 {
	return uint64(r.Regs.Rcx)
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

// Get returns the value of the n-th register (in x86asm order).
func (r *AMD64Registers) Get(n int) (uint64, error) {
	reg := x86asm.Reg(n)
	const (
		mask8  = 0x000000ff
		mask16 = 0x0000ffff
		mask32 = 0xffffffff
	)

	switch reg {
	// 8-bit
	case x86asm.AL:
		return uint64(r.Regs.Rax) & mask8, nil
	case x86asm.CL:
		return uint64(r.Regs.Rcx) & mask8, nil
	case x86asm.DL:
		return uint64(r.Regs.Rdx) & mask8, nil
	case x86asm.BL:
		return uint64(r.Regs.Rbx) & mask8, nil
	case x86asm.AH:
		return (uint64(r.Regs.Rax) >> 8) & mask8, nil
	case x86asm.CH:
		return (uint64(r.Regs.Rcx) >> 8) & mask8, nil
	case x86asm.DH:
		return (uint64(r.Regs.Rdx) >> 8) & mask8, nil
	case x86asm.BH:
		return (uint64(r.Regs.Rbx) >> 8) & mask8, nil
	case x86asm.SPB:
		return uint64(r.Regs.Rsp) & mask8, nil
	case x86asm.BPB:
		return uint64(r.Regs.Rbp) & mask8, nil
	case x86asm.SIB:
		return uint64(r.Regs.Rsi) & mask8, nil
	case x86asm.DIB:
		return uint64(r.Regs.Rdi) & mask8, nil
	case x86asm.R8B:
		return uint64(r.Regs.R8) & mask8, nil
	case x86asm.R9B:
		return uint64(r.Regs.R9) & mask8, nil
	case x86asm.R10B:
		return uint64(r.Regs.R10) & mask8, nil
	case x86asm.R11B:
		return uint64(r.Regs.R11) & mask8, nil
	case x86asm.R12B:
		return uint64(r.Regs.R12) & mask8, nil
	case x86asm.R13B:
		return uint64(r.Regs.R13) & mask8, nil
	case x86asm.R14B:
		return uint64(r.Regs.R14) & mask8, nil
	case x86asm.R15B:
		return uint64(r.Regs.R15) & mask8, nil

	// 16-bit
	case x86asm.AX:
		return uint64(r.Regs.Rax) & mask16, nil
	case x86asm.CX:
		return uint64(r.Regs.Rcx) & mask16, nil
	case x86asm.DX:
		return uint64(r.Regs.Rdx) & mask16, nil
	case x86asm.BX:
		return uint64(r.Regs.Rbx) & mask16, nil
	case x86asm.SP:
		return uint64(r.Regs.Rsp) & mask16, nil
	case x86asm.BP:
		return uint64(r.Regs.Rbp) & mask16, nil
	case x86asm.SI:
		return uint64(r.Regs.Rsi) & mask16, nil
	case x86asm.DI:
		return uint64(r.Regs.Rdi) & mask16, nil
	case x86asm.R8W:
		return uint64(r.Regs.R8) & mask16, nil
	case x86asm.R9W:
		return uint64(r.Regs.R9) & mask16, nil
	case x86asm.R10W:
		return uint64(r.Regs.R10) & mask16, nil
	case x86asm.R11W:
		return uint64(r.Regs.R11) & mask16, nil
	case x86asm.R12W:
		return uint64(r.Regs.R12) & mask16, nil
	case x86asm.R13W:
		return uint64(r.Regs.R13) & mask16, nil
	case x86asm.R14W:
		return uint64(r.Regs.R14) & mask16, nil
	case x86asm.R15W:
		return uint64(r.Regs.R15) & mask16, nil

	// 32-bit
	case x86asm.EAX:
		return uint64(r.Regs.Rax) & mask32, nil
	case x86asm.ECX:
		return uint64(r.Regs.Rcx) & mask32, nil
	case x86asm.EDX:
		return uint64(r.Regs.Rdx) & mask32, nil
	case x86asm.EBX:
		return uint64(r.Regs.Rbx) & mask32, nil
	case x86asm.ESP:
		return uint64(r.Regs.Rsp) & mask32, nil
	case x86asm.EBP:
		return uint64(r.Regs.Rbp) & mask32, nil
	case x86asm.ESI:
		return uint64(r.Regs.Rsi) & mask32, nil
	case x86asm.EDI:
		return uint64(r.Regs.Rdi) & mask32, nil
	case x86asm.R8L:
		return uint64(r.Regs.R8) & mask32, nil
	case x86asm.R9L:
		return uint64(r.Regs.R9) & mask32, nil
	case x86asm.R10L:
		return uint64(r.Regs.R10) & mask32, nil
	case x86asm.R11L:
		return uint64(r.Regs.R11) & mask32, nil
	case x86asm.R12L:
		return uint64(r.Regs.R12) & mask32, nil
	case x86asm.R13L:
		return uint64(r.Regs.R13) & mask32, nil
	case x86asm.R14L:
		return uint64(r.Regs.R14) & mask32, nil
	case x86asm.R15L:
		return uint64(r.Regs.R15) & mask32, nil

	// 64-bit
	case x86asm.RAX:
		return uint64(r.Regs.Rax), nil
	case x86asm.RCX:
		return uint64(r.Regs.Rcx), nil
	case x86asm.RDX:
		return uint64(r.Regs.Rdx), nil
	case x86asm.RBX:
		return uint64(r.Regs.Rbx), nil
	case x86asm.RSP:
		return uint64(r.Regs.Rsp), nil
	case x86asm.RBP:
		return uint64(r.Regs.Rbp), nil
	case x86asm.RSI:
		return uint64(r.Regs.Rsi), nil
	case x86asm.RDI:
		return uint64(r.Regs.Rdi), nil
	case x86asm.R8:
		return uint64(r.Regs.R8), nil
	case x86asm.R9:
		return uint64(r.Regs.R9), nil
	case x86asm.R10:
		return uint64(r.Regs.R10), nil
	case x86asm.R11:
		return uint64(r.Regs.R11), nil
	case x86asm.R12:
		return uint64(r.Regs.R12), nil
	case x86asm.R13:
		return uint64(r.Regs.R13), nil
	case x86asm.R14:
		return uint64(r.Regs.R14), nil
	case x86asm.R15:
		return uint64(r.Regs.R15), nil
	}

	return 0, proc.ErrUnknownRegister
}

// Copy returns a copy of these registers that is guarenteed not to change.
func (r *AMD64Registers) Copy() proc.Registers {
	var rr AMD64Registers
	rr.Regs = &AMD64PtraceRegs{}
	rr.Fpregset = &AMD64Xstate{}
	*(rr.Regs) = *(r.Regs)
	if r.Fpregset != nil {
		*(rr.Fpregset) = *(r.Fpregset)
	}
	if r.Fpregs != nil {
		rr.Fpregs = make([]proc.Register, len(r.Fpregs))
		copy(rr.Fpregs, r.Fpregs)
	}
	return &rr
}

type AMD64Xstate linutil.AMD64Xstate

func AMD64XstateRead(xstateargs []byte, readLegacy bool, regset *AMD64Xstate) error {
	return linutil.AMD64XstateRead(xstateargs, readLegacy, (*linutil.AMD64Xstate)(regset))
}

func (xsave *AMD64Xstate) Decode() (regs []proc.Register) {
	return (*linutil.AMD64Xstate).Decode((*linutil.AMD64Xstate)(xsave))
}
