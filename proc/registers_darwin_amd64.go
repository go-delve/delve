package proc

// #include "threads_darwin.h"
import "C"
import (
	"bytes"
	"fmt"

	"rsc.io/x86/x86asm"
)

// Regs represents CPU registers on an AMD64 processor.
type Regs struct {
	rax    uint64
	rbx    uint64
	rcx    uint64
	rdx    uint64
	rdi    uint64
	rsi    uint64
	rbp    uint64
	rsp    uint64
	r8     uint64
	r9     uint64
	r10    uint64
	r11    uint64
	r12    uint64
	r13    uint64
	r14    uint64
	r15    uint64
	rip    uint64
	rflags uint64
	cs     uint64
	fs     uint64
	gs     uint64
	gsBase uint64
}

func (r *Regs) String() string {
	var buf bytes.Buffer
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
		{"Rflags", r.rflags},
		{"Cs", r.cs},
		{"Fs", r.fs},
		{"Gs", r.gs},
		{"Gs_base", r.gsBase},
	}
	for _, reg := range regs {
		fmt.Fprintf(&buf, "%8s = %0#16x\n", reg.k, reg.v)
	}
	return buf.String()
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

// CX returns the value of the RCX register.
func (r *Regs) CX() uint64 {
	return r.rcx
}

// TLS returns the value of the register
// that contains the location of the thread
// local storage segment.
func (r *Regs) TLS() uint64 {
	return r.gsBase
}

// SetPC sets the RIP register to the value specified by `pc`.
func (r *Regs) SetPC(thread *Thread, pc uint64) error {
	kret := C.set_pc(thread.os.threadAct, C.uint64_t(pc))
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not set pc")
	}
	return nil
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
		return (r.rax >> 8) & mask8, nil
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

	return 0, UnknownRegisterError
}

func registers(thread *Thread) (Registers, error) {
	var state C.x86_thread_state64_t
	var identity C.thread_identifier_info_data_t
	kret := C.get_registers(C.mach_port_name_t(thread.os.threadAct), &state)
	if kret != C.KERN_SUCCESS {
		return nil, fmt.Errorf("could not get registers")
	}
	kret = C.get_identity(C.mach_port_name_t(thread.os.threadAct), &identity)
	if kret != C.KERN_SUCCESS {
		return nil, fmt.Errorf("could not get thread identity informations")
	}
	/*
		thread_identifier_info::thread_handle contains the base of the
		thread-specific data area, which on x86 and x86_64 is the thread’s base
		address of the %gs segment. 10.9.2 xnu-2422.90.20/osfmk/kern/thread.c
		thread_info_internal() gets the value from
		machine_thread::cthread_self, which is the same value used to set the
		%gs base in xnu-2422.90.20/osfmk/i386/pcb_native.c
		act_machine_switch_pcb().
		--
		comment copied from chromium's crashpad
		https://chromium.googlesource.com/crashpad/crashpad/+/master/snapshot/mac/process_reader.cc
	*/
	regs := &Regs{
		rax:    uint64(state.__rax),
		rbx:    uint64(state.__rbx),
		rcx:    uint64(state.__rcx),
		rdx:    uint64(state.__rdx),
		rdi:    uint64(state.__rdi),
		rsi:    uint64(state.__rsi),
		rbp:    uint64(state.__rbp),
		rsp:    uint64(state.__rsp),
		r8:     uint64(state.__r8),
		r9:     uint64(state.__r9),
		r10:    uint64(state.__r10),
		r11:    uint64(state.__r11),
		r12:    uint64(state.__r12),
		r13:    uint64(state.__r13),
		r14:    uint64(state.__r14),
		r15:    uint64(state.__r15),
		rip:    uint64(state.__rip),
		rflags: uint64(state.__rflags),
		cs:     uint64(state.__cs),
		fs:     uint64(state.__fs),
		gs:     uint64(state.__gs),
		gsBase: uint64(identity.thread_handle),
	}
	return regs, nil
}

func (t *Thread) saveRegisters() (Registers, error) {
	kret := C.get_registers(C.mach_port_name_t(t.os.threadAct), &thread.os.registers)
	if kret != C.KERN_SUCCESS {
		return nil, fmt.Errorf("could not save register contents")
	}
	return &Regs{rip: uint64(t.os.registers.__rip), rsp: uint64(t.os.registers.__rsp)}, nil
}

func (t *Thread) restoreRegisters() error {
	kret := C.set_registers(C.mach_port_name_t(t.os.threadAct), &tt.os.registers)
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not save register contents")
	}
	return nil
}
