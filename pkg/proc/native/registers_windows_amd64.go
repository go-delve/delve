package native

import (
	"fmt"
	"unsafe"

	"golang.org/x/arch/x86/x86asm"

	"github.com/derekparker/delve/pkg/proc"
)

// Regs represents CPU registers on an AMD64 processor.
type Regs struct {
	rax     uint64
	rbx     uint64
	rcx     uint64
	rdx     uint64
	rdi     uint64
	rsi     uint64
	rbp     uint64
	rsp     uint64
	r8      uint64
	r9      uint64
	r10     uint64
	r11     uint64
	r12     uint64
	r13     uint64
	r14     uint64
	r15     uint64
	rip     uint64
	eflags  uint64
	cs      uint64
	fs      uint64
	gs      uint64
	tls     uint64
	fltSave *_XMM_SAVE_AREA32
}

func (r *Regs) Slice() []proc.Register {
	var regs = []struct {
		k string
		v uint64
	}{
		{"Rip", r.rip},
		{"Rsp", r.rsp},
		{"Rax", r.rax},
		{"Rbx", r.rbx},
		{"Rcx", r.rcx},
		{"Rdx", r.rdx},
		{"Rdi", r.rdi},
		{"Rsi", r.rsi},
		{"Rbp", r.rbp},
		{"R8", r.r8},
		{"R9", r.r9},
		{"R10", r.r10},
		{"R11", r.r11},
		{"R12", r.r12},
		{"R13", r.r13},
		{"R14", r.r14},
		{"R15", r.r15},
		{"Eflags", r.eflags},
		{"Cs", r.cs},
		{"Fs", r.fs},
		{"Gs", r.gs},
		{"TLS", r.tls},
	}
	outlen := len(regs)
	if r.fltSave != nil {
		outlen += 6 + 8 + 2 + 16
	}
	out := make([]proc.Register, 0, outlen)
	for _, reg := range regs {
		if reg.k == "Eflags" {
			out = proc.AppendEflagReg(out, reg.k, reg.v)
		} else {
			out = proc.AppendQwordReg(out, reg.k, reg.v)
		}
	}
	if r.fltSave != nil {
		out = proc.AppendWordReg(out, "CW", r.fltSave.ControlWord)
		out = proc.AppendWordReg(out, "SW", r.fltSave.StatusWord)
		out = proc.AppendWordReg(out, "TW", uint16(r.fltSave.TagWord))
		out = proc.AppendWordReg(out, "FOP", r.fltSave.ErrorOpcode)
		out = proc.AppendQwordReg(out, "FIP", uint64(r.fltSave.ErrorSelector)<<32|uint64(r.fltSave.ErrorOffset))
		out = proc.AppendQwordReg(out, "FDP", uint64(r.fltSave.DataSelector)<<32|uint64(r.fltSave.DataOffset))

		for i := range r.fltSave.FloatRegisters {
			out = proc.AppendX87Reg(out, i, uint16(r.fltSave.FloatRegisters[i].High), r.fltSave.FloatRegisters[i].Low)
		}

		out = proc.AppendMxcsrReg(out, "MXCSR", uint64(r.fltSave.MxCsr))
		out = proc.AppendDwordReg(out, "MXCSR_MASK", r.fltSave.MxCsr_Mask)

		for i := 0; i < len(r.fltSave.XmmRegisters); i += 16 {
			out = proc.AppendSSEReg(out, fmt.Sprintf("XMM%d", i/16), r.fltSave.XmmRegisters[i:i+16])
		}
	}
	return out
}

// PC returns the current program counter
// i.e. the RIP CPU register.
func (r *Regs) PC() uint64 {
	return r.rip
}

// SP returns the stack pointer location,
// i.e. the RSP register.
func (r *Regs) SP() uint64 {
	return r.rsp
}

func (r *Regs) BP() uint64 {
	return r.rbp
}

// CX returns the value of the RCX register.
func (r *Regs) CX() uint64 {
	return r.rcx
}

// TLS returns the value of the register
// that contains the location of the thread
// local storage segment.
func (r *Regs) TLS() uint64 {
	return r.tls
}

func (r *Regs) GAddr() (uint64, bool) {
	return 0, false
}

// SetPC sets the RIP register to the value specified by `pc`.
func (thread *Thread) SetPC(pc uint64) error {
	context := newCONTEXT()
	context.ContextFlags = _CONTEXT_ALL

	err := _GetThreadContext(thread.os.hThread, context)
	if err != nil {
		return err
	}

	context.Rip = pc

	return _SetThreadContext(thread.os.hThread, context)
}

// SetSP sets the RSP register to the value specified by `sp`.
func (thread *Thread) SetSP(sp uint64) error {
	context := newCONTEXT()
	context.ContextFlags = _CONTEXT_ALL

	err := _GetThreadContext(thread.os.hThread, context)
	if err != nil {
		return err
	}

	context.Rsp = sp

	return _SetThreadContext(thread.os.hThread, context)
}

func (thread *Thread) SetDX(dx uint64) error {
	context := newCONTEXT()
	context.ContextFlags = _CONTEXT_ALL

	err := _GetThreadContext(thread.os.hThread, context)
	if err != nil {
		return err
	}

	context.Rdx = dx

	return _SetThreadContext(thread.os.hThread, context)
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
		return r.rax & mask8, nil
	case x86asm.CL:
		return r.rcx & mask8, nil
	case x86asm.DL:
		return r.rdx & mask8, nil
	case x86asm.BL:
		return r.rbx & mask8, nil
	case x86asm.AH:
		return (r.rax >> 8) & mask8, nil
	case x86asm.CH:
		return (r.rcx >> 8) & mask8, nil
	case x86asm.DH:
		return (r.rdx >> 8) & mask8, nil
	case x86asm.BH:
		return (r.rbx >> 8) & mask8, nil
	case x86asm.SPB:
		return r.rsp & mask8, nil
	case x86asm.BPB:
		return r.rbp & mask8, nil
	case x86asm.SIB:
		return r.rsi & mask8, nil
	case x86asm.DIB:
		return r.rdi & mask8, nil
	case x86asm.R8B:
		return r.r8 & mask8, nil
	case x86asm.R9B:
		return r.r9 & mask8, nil
	case x86asm.R10B:
		return r.r10 & mask8, nil
	case x86asm.R11B:
		return r.r11 & mask8, nil
	case x86asm.R12B:
		return r.r12 & mask8, nil
	case x86asm.R13B:
		return r.r13 & mask8, nil
	case x86asm.R14B:
		return r.r14 & mask8, nil
	case x86asm.R15B:
		return r.r15 & mask8, nil

	// 16-bit
	case x86asm.AX:
		return r.rax & mask16, nil
	case x86asm.CX:
		return r.rcx & mask16, nil
	case x86asm.DX:
		return r.rdx & mask16, nil
	case x86asm.BX:
		return r.rbx & mask16, nil
	case x86asm.SP:
		return r.rsp & mask16, nil
	case x86asm.BP:
		return r.rbp & mask16, nil
	case x86asm.SI:
		return r.rsi & mask16, nil
	case x86asm.DI:
		return r.rdi & mask16, nil
	case x86asm.R8W:
		return r.r8 & mask16, nil
	case x86asm.R9W:
		return r.r9 & mask16, nil
	case x86asm.R10W:
		return r.r10 & mask16, nil
	case x86asm.R11W:
		return r.r11 & mask16, nil
	case x86asm.R12W:
		return r.r12 & mask16, nil
	case x86asm.R13W:
		return r.r13 & mask16, nil
	case x86asm.R14W:
		return r.r14 & mask16, nil
	case x86asm.R15W:
		return r.r15 & mask16, nil

	// 32-bit
	case x86asm.EAX:
		return r.rax & mask32, nil
	case x86asm.ECX:
		return r.rcx & mask32, nil
	case x86asm.EDX:
		return r.rdx & mask32, nil
	case x86asm.EBX:
		return r.rbx & mask32, nil
	case x86asm.ESP:
		return r.rsp & mask32, nil
	case x86asm.EBP:
		return r.rbp & mask32, nil
	case x86asm.ESI:
		return r.rsi & mask32, nil
	case x86asm.EDI:
		return r.rdi & mask32, nil
	case x86asm.R8L:
		return r.r8 & mask32, nil
	case x86asm.R9L:
		return r.r9 & mask32, nil
	case x86asm.R10L:
		return r.r10 & mask32, nil
	case x86asm.R11L:
		return r.r11 & mask32, nil
	case x86asm.R12L:
		return r.r12 & mask32, nil
	case x86asm.R13L:
		return r.r13 & mask32, nil
	case x86asm.R14L:
		return r.r14 & mask32, nil
	case x86asm.R15L:
		return r.r15 & mask32, nil

	// 64-bit
	case x86asm.RAX:
		return r.rax, nil
	case x86asm.RCX:
		return r.rcx, nil
	case x86asm.RDX:
		return r.rdx, nil
	case x86asm.RBX:
		return r.rbx, nil
	case x86asm.RSP:
		return r.rsp, nil
	case x86asm.RBP:
		return r.rbp, nil
	case x86asm.RSI:
		return r.rsi, nil
	case x86asm.RDI:
		return r.rdi, nil
	case x86asm.R8:
		return r.r8, nil
	case x86asm.R9:
		return r.r9, nil
	case x86asm.R10:
		return r.r10, nil
	case x86asm.R11:
		return r.r11, nil
	case x86asm.R12:
		return r.r12, nil
	case x86asm.R13:
		return r.r13, nil
	case x86asm.R14:
		return r.r14, nil
	case x86asm.R15:
		return r.r15, nil
	}

	return 0, proc.UnknownRegisterError
}

func registers(thread *Thread, floatingPoint bool) (proc.Registers, error) {
	context := newCONTEXT()

	context.ContextFlags = _CONTEXT_ALL
	err := _GetThreadContext(thread.os.hThread, context)
	if err != nil {
		return nil, err
	}

	var threadInfo _THREAD_BASIC_INFORMATION
	status := _NtQueryInformationThread(thread.os.hThread, _ThreadBasicInformation, uintptr(unsafe.Pointer(&threadInfo)), uint32(unsafe.Sizeof(threadInfo)), nil)
	if !_NT_SUCCESS(status) {
		return nil, fmt.Errorf("NtQueryInformationThread failed: it returns 0x%x", status)
	}

	regs := &Regs{
		rax:    uint64(context.Rax),
		rbx:    uint64(context.Rbx),
		rcx:    uint64(context.Rcx),
		rdx:    uint64(context.Rdx),
		rdi:    uint64(context.Rdi),
		rsi:    uint64(context.Rsi),
		rbp:    uint64(context.Rbp),
		rsp:    uint64(context.Rsp),
		r8:     uint64(context.R8),
		r9:     uint64(context.R9),
		r10:    uint64(context.R10),
		r11:    uint64(context.R11),
		r12:    uint64(context.R12),
		r13:    uint64(context.R13),
		r14:    uint64(context.R14),
		r15:    uint64(context.R15),
		rip:    uint64(context.Rip),
		eflags: uint64(context.EFlags),
		cs:     uint64(context.SegCs),
		fs:     uint64(context.SegFs),
		gs:     uint64(context.SegGs),
		tls:    uint64(threadInfo.TebBaseAddress),
	}

	if floatingPoint {
		regs.fltSave = &context.FltSave
	}

	return regs, nil
}

type savedRegisters struct {
}

func (r *Regs) Save() proc.SavedRegisters {
	//TODO(aarzilli): implement this to support function calls
	return nil
}
