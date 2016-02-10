package proc

// #include <windows.h>
import "C"
import (
	"bytes"
	"fmt"
	"syscall"
	"unsafe"
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
	eflags uint64
	cs     uint64
	fs     uint64
	gs     uint64
	tls    uint64
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
		{"Eflags", r.eflags},
		{"Cs", r.cs},
		{"Fs", r.fs},
		{"Gs", r.gs},
		{"TLS", r.tls},
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
	return r.tls
}

// SetPC sets the RIP register to the value specified by `pc`.
func (r *Regs) SetPC(thread *Thread, pc uint64) error {
	var context C.CONTEXT
	context.ContextFlags = C.CONTEXT_ALL

	res := C.GetThreadContext(C.HANDLE(thread.os.hThread), &context)
	if res == C.FALSE {
		return fmt.Errorf("could not GetThreadContext")
	}

	context.Rip = C.DWORD64(pc)

	res = C.SetThreadContext(C.HANDLE(thread.os.hThread), &context)
	if res == C.FALSE {
		return fmt.Errorf("could not SetThreadContext")
	}

	return nil
}

func registers(thread *Thread) (Registers, error) {
	var context C.CONTEXT

	context.ContextFlags = C.CONTEXT_ALL
	res := C.GetThreadContext(C.HANDLE(thread.os.hThread), &context)
	if res == C.FALSE {
		return nil, fmt.Errorf("failed to read ThreadContext")
	}

	var threadInfo _THREAD_BASIC_INFORMATION
	status := _NtQueryInformationThread(syscall.Handle(thread.os.hThread), _ThreadBasicInformation, uintptr(unsafe.Pointer(&threadInfo)), uint32(unsafe.Sizeof(threadInfo)), nil)
	if !_NT_SUCCESS(status) {
		return nil, fmt.Errorf("failed to get thread_basic_information")
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
	return regs, nil
}

func (thread *Thread) saveRegisters() (Registers, error) {
	return nil, fmt.Errorf("not implemented: saveRegisters")
}

func (thread *Thread) restoreRegisters() error {
	return fmt.Errorf("not implemented: restoreRegisters")
}
