package native

import (
	"fmt"

	"golang.org/x/arch/x86/x86asm"
	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/pkg/proc"
)

// Regs is a wrapper for sys.PtraceRegs.
type Regs struct {
	regs     *sys.PtraceRegs
	fpregs   []proc.Register
	fpregset *proc.LinuxX86Xstate
}

func (r *Regs) Slice() []proc.Register {
	var regs = []struct {
		k string
		v uint64
	}{
		{"Rip", r.regs.Rip},
		{"Rsp", r.regs.Rsp},
		{"Rax", r.regs.Rax},
		{"Rbx", r.regs.Rbx},
		{"Rcx", r.regs.Rcx},
		{"Rdx", r.regs.Rdx},
		{"Rdi", r.regs.Rdi},
		{"Rsi", r.regs.Rsi},
		{"Rbp", r.regs.Rbp},
		{"R8", r.regs.R8},
		{"R9", r.regs.R9},
		{"R10", r.regs.R10},
		{"R11", r.regs.R11},
		{"R12", r.regs.R12},
		{"R13", r.regs.R13},
		{"R14", r.regs.R14},
		{"R15", r.regs.R15},
		{"Orig_rax", r.regs.Orig_rax},
		{"Cs", r.regs.Cs},
		{"Eflags", r.regs.Eflags},
		{"Ss", r.regs.Ss},
		{"Fs_base", r.regs.Fs_base},
		{"Gs_base", r.regs.Gs_base},
		{"Ds", r.regs.Ds},
		{"Es", r.regs.Es},
		{"Fs", r.regs.Fs},
		{"Gs", r.regs.Gs},
	}
	out := make([]proc.Register, 0, len(regs)+len(r.fpregs))
	for _, reg := range regs {
		if reg.k == "Eflags" {
			out = proc.AppendEflagReg(out, reg.k, reg.v)
		} else {
			out = proc.AppendQwordReg(out, reg.k, reg.v)
		}
	}
	out = append(out, r.fpregs...)
	return out
}

// PC returns the value of RIP register.
func (r *Regs) PC() uint64 {
	return r.regs.PC()
}

// SP returns the value of RSP register.
func (r *Regs) SP() uint64 {
	return r.regs.Rsp
}

func (r *Regs) BP() uint64 {
	return r.regs.Rbp
}

// CX returns the value of RCX register.
func (r *Regs) CX() uint64 {
	return r.regs.Rcx
}

// TLS returns the address of the thread
// local storage memory segment.
func (r *Regs) TLS() uint64 {
	return r.regs.Fs_base
}

func (r *Regs) GAddr() (uint64, bool) {
	return 0, false
}

// SetPC sets RIP to the value specified by 'pc'.
func (thread *Thread) SetPC(pc uint64) error {
	ir, err := registers(thread, false)
	if err != nil {
		return err
	}
	r := ir.(*Regs)
	r.regs.SetPC(pc)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.ID, r.regs) })
	return err
}

// SetSP sets RSP to the value specified by 'sp'
func (thread *Thread) SetSP(sp uint64) (err error) {
	var ir proc.Registers
	ir, err = registers(thread, false)
	if err != nil {
		return err
	}
	r := ir.(*Regs)
	r.regs.Rsp = sp
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.ID, r.regs) })
	return
}

func (thread *Thread) SetDX(dx uint64) (err error) {
	var ir proc.Registers
	ir, err = registers(thread, false)
	if err != nil {
		return err
	}
	r := ir.(*Regs)
	r.regs.Rdx = dx
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(thread.ID, r.regs) })
	return
}

func (r *Regs) Get(n int) (uint64, error) {
	reg := x86asm.Reg(n)
	const (
		mask8  = 0x000f
		mask16 = 0x00ff
		mask32 = 0xffff
	)

	switch reg {
	// 8-bit
	case x86asm.AL:
		return r.regs.Rax & mask8, nil
	case x86asm.CL:
		return r.regs.Rcx & mask8, nil
	case x86asm.DL:
		return r.regs.Rdx & mask8, nil
	case x86asm.BL:
		return r.regs.Rbx & mask8, nil
	case x86asm.AH:
		return (r.regs.Rax >> 8) & mask8, nil
	case x86asm.CH:
		return (r.regs.Rcx >> 8) & mask8, nil
	case x86asm.DH:
		return (r.regs.Rdx >> 8) & mask8, nil
	case x86asm.BH:
		return (r.regs.Rbx >> 8) & mask8, nil
	case x86asm.SPB:
		return r.regs.Rsp & mask8, nil
	case x86asm.BPB:
		return r.regs.Rbp & mask8, nil
	case x86asm.SIB:
		return r.regs.Rsi & mask8, nil
	case x86asm.DIB:
		return r.regs.Rdi & mask8, nil
	case x86asm.R8B:
		return r.regs.R8 & mask8, nil
	case x86asm.R9B:
		return r.regs.R9 & mask8, nil
	case x86asm.R10B:
		return r.regs.R10 & mask8, nil
	case x86asm.R11B:
		return r.regs.R11 & mask8, nil
	case x86asm.R12B:
		return r.regs.R12 & mask8, nil
	case x86asm.R13B:
		return r.regs.R13 & mask8, nil
	case x86asm.R14B:
		return r.regs.R14 & mask8, nil
	case x86asm.R15B:
		return r.regs.R15 & mask8, nil

	// 16-bit
	case x86asm.AX:
		return r.regs.Rax & mask16, nil
	case x86asm.CX:
		return r.regs.Rcx & mask16, nil
	case x86asm.DX:
		return r.regs.Rdx & mask16, nil
	case x86asm.BX:
		return r.regs.Rbx & mask16, nil
	case x86asm.SP:
		return r.regs.Rsp & mask16, nil
	case x86asm.BP:
		return r.regs.Rbp & mask16, nil
	case x86asm.SI:
		return r.regs.Rsi & mask16, nil
	case x86asm.DI:
		return r.regs.Rdi & mask16, nil
	case x86asm.R8W:
		return r.regs.R8 & mask16, nil
	case x86asm.R9W:
		return r.regs.R9 & mask16, nil
	case x86asm.R10W:
		return r.regs.R10 & mask16, nil
	case x86asm.R11W:
		return r.regs.R11 & mask16, nil
	case x86asm.R12W:
		return r.regs.R12 & mask16, nil
	case x86asm.R13W:
		return r.regs.R13 & mask16, nil
	case x86asm.R14W:
		return r.regs.R14 & mask16, nil
	case x86asm.R15W:
		return r.regs.R15 & mask16, nil

	// 32-bit
	case x86asm.EAX:
		return r.regs.Rax & mask32, nil
	case x86asm.ECX:
		return r.regs.Rcx & mask32, nil
	case x86asm.EDX:
		return r.regs.Rdx & mask32, nil
	case x86asm.EBX:
		return r.regs.Rbx & mask32, nil
	case x86asm.ESP:
		return r.regs.Rsp & mask32, nil
	case x86asm.EBP:
		return r.regs.Rbp & mask32, nil
	case x86asm.ESI:
		return r.regs.Rsi & mask32, nil
	case x86asm.EDI:
		return r.regs.Rdi & mask32, nil
	case x86asm.R8L:
		return r.regs.R8 & mask32, nil
	case x86asm.R9L:
		return r.regs.R9 & mask32, nil
	case x86asm.R10L:
		return r.regs.R10 & mask32, nil
	case x86asm.R11L:
		return r.regs.R11 & mask32, nil
	case x86asm.R12L:
		return r.regs.R12 & mask32, nil
	case x86asm.R13L:
		return r.regs.R13 & mask32, nil
	case x86asm.R14L:
		return r.regs.R14 & mask32, nil
	case x86asm.R15L:
		return r.regs.R15 & mask32, nil

	// 64-bit
	case x86asm.RAX:
		return r.regs.Rax, nil
	case x86asm.RCX:
		return r.regs.Rcx, nil
	case x86asm.RDX:
		return r.regs.Rdx, nil
	case x86asm.RBX:
		return r.regs.Rbx, nil
	case x86asm.RSP:
		return r.regs.Rsp, nil
	case x86asm.RBP:
		return r.regs.Rbp, nil
	case x86asm.RSI:
		return r.regs.Rsi, nil
	case x86asm.RDI:
		return r.regs.Rdi, nil
	case x86asm.R8:
		return r.regs.R8, nil
	case x86asm.R9:
		return r.regs.R9, nil
	case x86asm.R10:
		return r.regs.R10, nil
	case x86asm.R11:
		return r.regs.R11, nil
	case x86asm.R12:
		return r.regs.R12, nil
	case x86asm.R13:
		return r.regs.R13, nil
	case x86asm.R14:
		return r.regs.R14, nil
	case x86asm.R15:
		return r.regs.R15, nil
	}

	return 0, proc.UnknownRegisterError
}

func registers(thread *Thread, floatingPoint bool) (proc.Registers, error) {
	var (
		regs sys.PtraceRegs
		err  error
	)
	thread.dbp.execPtraceFunc(func() { err = sys.PtraceGetRegs(thread.ID, &regs) })
	if err != nil {
		return nil, err
	}
	r := &Regs{&regs, nil, nil}
	if floatingPoint {
		var fpregset proc.LinuxX86Xstate
		r.fpregs, fpregset, err = thread.fpRegisters()
		r.fpregset = &fpregset
		if err != nil {
			return nil, err
		}
	}
	return r, nil
}

const (
	_X86_XSTATE_MAX_SIZE = 2688
	_NT_X86_XSTATE       = 0x202

	_XSAVE_HEADER_START          = 512
	_XSAVE_HEADER_LEN            = 64
	_XSAVE_EXTENDED_REGION_START = 576
	_XSAVE_SSE_REGION_LEN        = 416
)

func (thread *Thread) fpRegisters() (regs []proc.Register, fpregs proc.LinuxX86Xstate, err error) {
	thread.dbp.execPtraceFunc(func() { fpregs, err = PtraceGetRegset(thread.ID) })
	regs = fpregs.Decode()
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}
	return
}

func (r *Regs) Copy() proc.Registers {
	return r
}
