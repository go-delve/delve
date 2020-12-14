package linutil

import (
	"golang.org/x/arch/x86/x86asm"

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
		return r.Regs.Rax & mask8, nil
	case x86asm.CL:
		return r.Regs.Rcx & mask8, nil
	case x86asm.DL:
		return r.Regs.Rdx & mask8, nil
	case x86asm.BL:
		return r.Regs.Rbx & mask8, nil
	case x86asm.AH:
		return (r.Regs.Rax >> 8) & mask8, nil
	case x86asm.CH:
		return (r.Regs.Rcx >> 8) & mask8, nil
	case x86asm.DH:
		return (r.Regs.Rdx >> 8) & mask8, nil
	case x86asm.BH:
		return (r.Regs.Rbx >> 8) & mask8, nil
	case x86asm.SPB:
		return r.Regs.Rsp & mask8, nil
	case x86asm.BPB:
		return r.Regs.Rbp & mask8, nil
	case x86asm.SIB:
		return r.Regs.Rsi & mask8, nil
	case x86asm.DIB:
		return r.Regs.Rdi & mask8, nil
	case x86asm.R8B:
		return r.Regs.R8 & mask8, nil
	case x86asm.R9B:
		return r.Regs.R9 & mask8, nil
	case x86asm.R10B:
		return r.Regs.R10 & mask8, nil
	case x86asm.R11B:
		return r.Regs.R11 & mask8, nil
	case x86asm.R12B:
		return r.Regs.R12 & mask8, nil
	case x86asm.R13B:
		return r.Regs.R13 & mask8, nil
	case x86asm.R14B:
		return r.Regs.R14 & mask8, nil
	case x86asm.R15B:
		return r.Regs.R15 & mask8, nil

	// 16-bit
	case x86asm.AX:
		return r.Regs.Rax & mask16, nil
	case x86asm.CX:
		return r.Regs.Rcx & mask16, nil
	case x86asm.DX:
		return r.Regs.Rdx & mask16, nil
	case x86asm.BX:
		return r.Regs.Rbx & mask16, nil
	case x86asm.SP:
		return r.Regs.Rsp & mask16, nil
	case x86asm.BP:
		return r.Regs.Rbp & mask16, nil
	case x86asm.SI:
		return r.Regs.Rsi & mask16, nil
	case x86asm.DI:
		return r.Regs.Rdi & mask16, nil
	case x86asm.R8W:
		return r.Regs.R8 & mask16, nil
	case x86asm.R9W:
		return r.Regs.R9 & mask16, nil
	case x86asm.R10W:
		return r.Regs.R10 & mask16, nil
	case x86asm.R11W:
		return r.Regs.R11 & mask16, nil
	case x86asm.R12W:
		return r.Regs.R12 & mask16, nil
	case x86asm.R13W:
		return r.Regs.R13 & mask16, nil
	case x86asm.R14W:
		return r.Regs.R14 & mask16, nil
	case x86asm.R15W:
		return r.Regs.R15 & mask16, nil

	// 32-bit
	case x86asm.EAX:
		return r.Regs.Rax & mask32, nil
	case x86asm.ECX:
		return r.Regs.Rcx & mask32, nil
	case x86asm.EDX:
		return r.Regs.Rdx & mask32, nil
	case x86asm.EBX:
		return r.Regs.Rbx & mask32, nil
	case x86asm.ESP:
		return r.Regs.Rsp & mask32, nil
	case x86asm.EBP:
		return r.Regs.Rbp & mask32, nil
	case x86asm.ESI:
		return r.Regs.Rsi & mask32, nil
	case x86asm.EDI:
		return r.Regs.Rdi & mask32, nil
	case x86asm.R8L:
		return r.Regs.R8 & mask32, nil
	case x86asm.R9L:
		return r.Regs.R9 & mask32, nil
	case x86asm.R10L:
		return r.Regs.R10 & mask32, nil
	case x86asm.R11L:
		return r.Regs.R11 & mask32, nil
	case x86asm.R12L:
		return r.Regs.R12 & mask32, nil
	case x86asm.R13L:
		return r.Regs.R13 & mask32, nil
	case x86asm.R14L:
		return r.Regs.R14 & mask32, nil
	case x86asm.R15L:
		return r.Regs.R15 & mask32, nil

	// 64-bit
	case x86asm.RAX:
		return r.Regs.Rax, nil
	case x86asm.RCX:
		return r.Regs.Rcx, nil
	case x86asm.RDX:
		return r.Regs.Rdx, nil
	case x86asm.RBX:
		return r.Regs.Rbx, nil
	case x86asm.RSP:
		return r.Regs.Rsp, nil
	case x86asm.RBP:
		return r.Regs.Rbp, nil
	case x86asm.RSI:
		return r.Regs.Rsi, nil
	case x86asm.RDI:
		return r.Regs.Rdi, nil
	case x86asm.R8:
		return r.Regs.R8, nil
	case x86asm.R9:
		return r.Regs.R9, nil
	case x86asm.R10:
		return r.Regs.R10, nil
	case x86asm.R11:
		return r.Regs.R11, nil
	case x86asm.R12:
		return r.Regs.R12, nil
	case x86asm.R13:
		return r.Regs.R13, nil
	case x86asm.R14:
		return r.Regs.R14, nil
	case x86asm.R15:
		return r.Regs.R15, nil
	}

	return 0, proc.ErrUnknownRegister
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
