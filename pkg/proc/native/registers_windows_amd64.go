package native

import (
	"fmt"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/winutil"
)

// SetPC sets the RIP register to the value specified by `pc`.
func (thread *nativeThread) setPC(pc uint64) error {
	context := winutil.NewCONTEXT()
	context.ContextFlags = _CONTEXT_ALL

	err := _GetThreadContext(thread.os.hThread, context)
	if err != nil {
		return err
	}

	context.Rip = pc

	return _SetThreadContext(thread.os.hThread, context)
}

// SetReg changes the value of the specified register.
func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	context := winutil.NewCONTEXT()
	context.ContextFlags = _CONTEXT_ALL

	err := _GetThreadContext(thread.os.hThread, context)
	if err != nil {
		return err
	}

	var p *uint64

	switch regNum {
	case regnum.AMD64_Rax:
		p = &context.Rax
	case regnum.AMD64_Rbx:
		p = &context.Rbx
	case regnum.AMD64_Rcx:
		p = &context.Rcx
	case regnum.AMD64_Rdx:
		p = &context.Rdx
	case regnum.AMD64_Rsi:
		p = &context.Rsi
	case regnum.AMD64_Rdi:
		p = &context.Rdi
	case regnum.AMD64_Rbp:
		p = &context.Rbp
	case regnum.AMD64_Rsp:
		p = &context.Rsp
	case regnum.AMD64_R8:
		p = &context.R8
	case regnum.AMD64_R9:
		p = &context.R9
	case regnum.AMD64_R10:
		p = &context.R10
	case regnum.AMD64_R11:
		p = &context.R11
	case regnum.AMD64_R12:
		p = &context.R12
	case regnum.AMD64_R13:
		p = &context.R13
	case regnum.AMD64_R14:
		p = &context.R14
	case regnum.AMD64_R15:
		p = &context.R15
	case regnum.AMD64_Rip:
		p = &context.Rip
	}

	if p != nil {
		if reg.Bytes != nil && len(reg.Bytes) != 8 {
			return fmt.Errorf("wrong number of bytes for register %s (%d)", regnum.AMD64ToName(regNum), len(reg.Bytes))
		}
		*p = reg.Uint64Val
	} else {
		if regNum < regnum.AMD64_XMM0 || regNum > regnum.AMD64_XMM0+15 {
			return fmt.Errorf("can not set register %s", regnum.AMD64ToName(regNum))
		}
		reg.FillBytes()
		if len(reg.Bytes) > 16 {
			return fmt.Errorf("too many bytes when setting register %s", regnum.AMD64ToName(regNum))
		}
		copy(context.FltSave.XmmRegisters[(regNum-regnum.AMD64_XMM0)*16:], reg.Bytes)
	}

	return _SetThreadContext(thread.os.hThread, context)
}

func registers(thread *nativeThread) (proc.Registers, error) {
	context := winutil.NewCONTEXT()

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

	return winutil.NewAMD64Registers(context, uint64(threadInfo.TebBaseAddress)), nil
}
