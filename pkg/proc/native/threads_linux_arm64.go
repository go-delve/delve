package native

import (
	"debug/elf"
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

func (thread *nativeThread) fpRegisters() ([]proc.Register, []byte, error) {
	var err error
	var arm_fpregs linutil.ARM64PtraceFpRegs
	thread.dbp.execPtraceFunc(func() { arm_fpregs.Vregs, err = ptraceGetFpRegset(thread.ID) })
	fpregs := arm_fpregs.Decode()
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}
	return fpregs, arm_fpregs.Vregs, err
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	sr := savedRegs.(*linutil.ARM64Registers)

	var restoreRegistersErr error
	t.dbp.execPtraceFunc(func() {
		restoreRegistersErr = ptraceSetGRegs(t.ID, sr.Regs)
		if restoreRegistersErr != syscall.Errno(0) && restoreRegistersErr != nil {
			return
		}
		if sr.Fpregset != nil {
			iov := sys.Iovec{Base: &sr.Fpregset[0], Len: uint64(len(sr.Fpregset))}
			_, _, restoreRegistersErr = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(t.ID), uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
		}
	})
	if restoreRegistersErr == syscall.Errno(0) {
		restoreRegistersErr = nil
	}
	return restoreRegistersErr
}

const (
	_MAX_ARM64_WATCH = 16
	_NT_ARM_HW_WATCH = 0x403
	_TRAP_HWBKPT     = 0x4
)

type watchpointState struct {
	num      uint8
	debugVer uint8
	words    []uint64
}

func (wpstate *watchpointState) set(idx uint8, addr, ctrl uint64) {
	wpstate.words[1+idx*2] = addr
	wpstate.words[1+idx*2+1] = ctrl
}

// getWatchpoints reads the NT_ARM_HW_WATCH ptrace register set.
// The format of this register set is described by user_hwdebug_state in
// arch/arm64/include/uapi/asm/ptrace.h.
// It consists of one 64bit word containing:
//   - 1byte number of watchpoints
//   - 1byte debug architecture version (the 4 least significant bits of ID_AA64DFR0_EL1)
//   - 6bytes padding
//
// Followed by 2 64bit words for each watchpoint, up to a maximum of 16
// watchpoints. The first word contains the address at which the watchpoint
// is set (DBGWVRn_EL1), the second word is the control register for the
// watchpoint (DBGWCRn_EL1), as described by
// ARM - Architecture Reference Manual Armv8, for A-profile architectures
// section D13.3.11
// where only the BAS, LSC, PAC and E fields are accessible.
func (t *nativeThread) getWatchpoints() (*watchpointState, error) {
	words := make([]uint64, _MAX_ARM64_WATCH*2+1)
	iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(&words[0])), Len: uint64(len(words)) * uint64(unsafe.Sizeof(words[0]))}
	var err error
	t.dbp.execPtraceFunc(func() {
		_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETREGSET, uintptr(t.ID), _NT_ARM_HW_WATCH, uintptr(unsafe.Pointer(&iov)), 0, 0)
	})
	if err != syscall.Errno(0) {
		return nil, err
	}
	wpstate := &watchpointState{num: uint8(words[0] & 0xff), debugVer: uint8((words[0] >> 8) & 0xff), words: words}
	if wpstate.num > _MAX_ARM64_WATCH {
		// According to the specification this should never be more than 16 but
		// the code here will not work if this limit ever gets relaxed.
		wpstate.num = _MAX_ARM64_WATCH
	}
	// remove the watchpoint registers that don't exist from the words slice so
	// that we won't try later to write them (which would cause an ENOSPC
	// error).
	wpstate.words = wpstate.words[:wpstate.num*2+1]
	return wpstate, nil
}

// setWatchpoints saves the watchpoint state of the given thread.
// See (*nativeThread).getWatchpoints for a description of how this works.
func (t *nativeThread) setWatchpoints(wpstate *watchpointState) error {
	iov := sys.Iovec{Base: (*byte)(unsafe.Pointer(&(wpstate.words[0]))), Len: uint64(len(wpstate.words)) * uint64(unsafe.Sizeof(wpstate.words[0]))}
	var err error
	t.dbp.execPtraceFunc(func() {
		_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(t.ID), _NT_ARM_HW_WATCH, uintptr(unsafe.Pointer(&iov)), 0, 0)
	})
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}

type ptraceSiginfoArm64 struct {
	signo uint32
	errno uint32
	code  uint32
	addr  uint64    // only valid if Signo is SIGTRAP, SIGFPE, SIGILL, SIGBUS or SIGEMT
	pad   [128]byte // the total size of siginfo_t on ARM64 is 128 bytes so this is more than enough padding for all the fields we don't care about
}

func (t *nativeThread) findHardwareBreakpoint() (*proc.Breakpoint, error) {
	var siginfo ptraceSiginfoArm64
	var err error
	t.dbp.execPtraceFunc(func() {
		_, _, err = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_GETSIGINFO, uintptr(t.ID), 0, uintptr(unsafe.Pointer(&siginfo)), 0, 0)
	})
	if err != syscall.Errno(0) {
		return nil, err
	}
	if siginfo.signo != uint32(sys.SIGTRAP) || (siginfo.code&0xffff) != _TRAP_HWBKPT {
		return nil, nil
	}

	for _, bp := range t.dbp.Breakpoints().M {
		if bp.WatchType != 0 && siginfo.addr >= bp.Addr && siginfo.addr < bp.Addr+uint64(bp.WatchType.Size()) {
			return bp, nil
		}
	}

	return nil, fmt.Errorf("could not find hardware breakpoint for address %#x", siginfo.addr)
}

func (t *nativeThread) writeHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	wpstate, err := t.getWatchpoints()
	if err != nil {
		return err
	}
	if idx >= wpstate.num {
		return errors.New("hardware breakpoints exhausted")
	}

	const (
		readBreakpoint  = 0x1
		writeBreakpoint = 0x2
		lenBitOffset    = 5
		typeBitOffset   = 3
		privBitOffset   = 1
	)

	var typ uint64
	if wtype.Read() {
		typ |= readBreakpoint
	}
	if wtype.Write() {
		typ |= writeBreakpoint
	}

	len := uint64((1 << wtype.Size()) - 1) // arm wants the length expressed as address bitmask

	priv := uint64(3)

	ctrl := (len << lenBitOffset) | (typ << typeBitOffset) | (priv << privBitOffset) | 1
	wpstate.set(idx, addr, ctrl)

	return t.setWatchpoints(wpstate)
}

func (t *nativeThread) clearHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	wpstate, err := t.getWatchpoints()
	if err != nil {
		return err
	}
	if idx >= wpstate.num {
		return errors.New("hardware breakpoints exhausted")
	}
	wpstate.set(idx, 0, 0)
	return t.setWatchpoints(wpstate)
}
