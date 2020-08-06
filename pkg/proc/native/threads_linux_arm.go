package native

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
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
			nextInstrLen := t.BinInfo().Arch.MaxInstructionLength()
			nextInstrBytes := make([]byte, nextInstrLen)
			t.dbp.execPtraceFunc(func() {
				_, err = sys.PtracePeekData(t.ID, uintptr(regs.PC()), nextInstrBytes)
			})
			if err != nil {
				return nil, err
			}
			nextPcs := []uint64{
				regs.PC() + uint64(nextInstrLen),
			}
			// If we found breakpoint, we just skip it.
			if bytes.Equal(nextInstrBytes, t.BinInfo().Arch.BreakpointInstruction()) {
				return nextPcs, nil
			}
			// Golang always use ARM mode.
			nextInstr, err := armasm.Decode(nextInstrBytes, armasm.ModeARM)
			if err != nil {
				return nil, err
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
			case armasm.POP:
				if regList, ok := nextInstr.Args[0].(armasm.RegList); ok && (regList&(1<<uint(armasm.PC)) != 0) {
					pc, err := regs.Get(int(armasm.SP))
					if err != nil {
						return nil, err
					}
					for i := 0; i < int(armasm.PC); i++ {
						if regList&(1<<uint(i)) != 0 {
							pc += uint64(nextInstrLen)
						}
					}
					pcMem := make([]byte, nextInstrLen)
					t.dbp.execPtraceFunc(func() {
						_, err = sys.PtracePeekData(t.ID, uintptr(pc), pcMem)
					})
					if err != nil {
						return nil, err
					}
					nextPcs = append(nextPcs, uint64(binary.LittleEndian.Uint32(pcMem)))
				}
			case armasm.LDR:
				// We need to check for the first args to be PC.
				if reg, ok := nextInstr.Args[0].(armasm.Reg); ok && reg == armasm.PC {
					var pc uint64
					for _, argRaw := range nextInstr.Args[1:] {
						switch arg := argRaw.(type) {
						case armasm.Imm:
							pc += uint64(arg)
						case armasm.Reg:
							regVal, err := regs.Get(int(arg))
							if err != nil {
								return nil, err
							}
							pc += regVal
						}
					}
					pcMem := make([]byte, nextInstrLen)
					t.dbp.execPtraceFunc(func() {
						_, err = sys.PtracePeekData(t.ID, uintptr(pc), pcMem)
					})
					if err != nil {
						return nil, err
					}
					nextPcs = append(nextPcs, uint64(binary.LittleEndian.Uint32(pcMem)))
				}
				fallthrough
			case armasm.MOV, armasm.ADD:
				// We need to check for the first args to be PC.
				if reg, ok := nextInstr.Args[0].(armasm.Reg); ok && reg == armasm.PC {
					var pc uint64
					for _, argRaw := range nextInstr.Args[1:] {
						switch arg := argRaw.(type) {
						case armasm.Imm:
							pc += uint64(arg)
						case armasm.Reg:
							regVal, err := regs.Get(int(arg))
							if err != nil {
								return nil, err
							}
							pc += regVal
						}
					}
					nextPcs = append(nextPcs, pc)
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
		t.dbp.execPtraceFunc(func() { err = ptraceCont(t.ID, 0) })
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
