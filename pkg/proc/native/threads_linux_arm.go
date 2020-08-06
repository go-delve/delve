package native

import (
	"debug/elf"
	"fmt"
	"golang.org/x/arch/arm/armasm"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

func (t *nativeThread) fpRegisters() ([]proc.Register, []byte, error) {
	var err error
	var arm_fpregs linutil.ARM64PtraceFpRegs
	t.dbp.execPtraceFunc(func() { arm_fpregs.Vregs, err = ptraceGetFpRegset(t.ID) })
	fpregs := arm_fpregs.Decode()
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}
	return fpregs, arm_fpregs.Vregs, err
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	sr := savedRegs.(*linutil.ARMRegisters)

	var restoreRegistersErr error
	t.dbp.execPtraceFunc(func() {
		restoreRegistersErr = ptraceSetGRegs(t.ID, sr.Regs)
		if restoreRegistersErr != syscall.Errno(0) {
			return
		}
		if sr.Fpregset != nil {
			iov := sys.Iovec{Base: &sr.Fpregset[0], Len: uint32(len(sr.Fpregset))}
			_, _, restoreRegistersErr = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(t.ID), uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
		}
	})
	if restoreRegistersErr == syscall.Errno(0) {
		restoreRegistersErr = nil
	}
	return restoreRegistersErr
}

func (t *nativeThread) singleStep() (err error) {
	// Arm don't have ptrace singleStep implemented, so we use breakpoint to emulate it.
	for {
		regs, err := t.Registers()
		if err != nil {
			return err
		}
		resolvePCs := func() ([]uint64, error) {
			// Use ptrace to get better performance.
			nextInstLen := t.BinInfo().Arch.MaxInstructionLength()
			nextInstrBytes := make([]byte, nextInstLen)
			t.dbp.execPtraceFunc(func() {
				_, err = sys.PtracePeekData(t.ID, uintptr(regs.PC()), nextInstrBytes)
			})
			if err != nil {
				return nil, err
			}
			// Golang always use ARM mode.
			nextInstr, err := armasm.Decode(nextInstrBytes, armasm.ModeARM)
			if err != nil {
				return nil, err
			}
			nextPcs := []uint64{
				regs.PC() + uint64(nextInstLen),
			}
			switch nextInstr.Op {
			case armasm.BL, armasm.BLX, armasm.B, armasm.BX:
				switch arg := nextInstr.Args[0].(type) {
				case armasm.Imm:
					nextPcs = append(nextPcs, uint64(arg))
				case armasm.Reg:
					pc, err := regs.Get(int(arg))
					if err != nil {
						return nil, err
					}
					nextPcs = append(nextPcs, pc)
				case armasm.PCRel:
					nextPcs = append(nextPcs, regs.PC()+uint64(arg))
				}
			}
			return nextPcs, nil
		}
		nextPcs, err := resolvePCs()
		if err != nil {
			return err
		}
		originalDatas := make([][]byte, len(nextPcs))
		// Make sure we restore before return.
		defer func() {
			// Update err.
			t.dbp.execPtraceFunc(func() {
				for i, originalData := range originalDatas {
					if originalData != nil {
						_, err = sys.PtracePokeData(t.ID, uintptr(nextPcs[i]), originalData)
					}
				}
			})
		}()
		for i, nextPc := range nextPcs {
			_, _, _, originalDatas[i], err = t.dbp.WriteBreakpoint(nextPc)
			if err != nil {
				return err
			}
		}
		t.dbp.execPtraceFunc(func() { err = sys.PtraceSyscall(t.ID, 0) })
		if err != nil {
			return err
		}
		wpid, status, err := t.dbp.waitFast(t.ID)
		if err != nil {
			return err
		}
		if (status == nil || status.Exited()) && wpid == t.dbp.pid {
			t.dbp.postExit()
			rs := 0
			if status != nil {
				rs = status.ExitStatus()
			}
			return proc.ErrProcessExited{Pid: t.dbp.pid, Status: rs}
		}
		if wpid == t.ID && status.StopSignal() == sys.SIGTRAP {
			return nil
		}
	}
}
