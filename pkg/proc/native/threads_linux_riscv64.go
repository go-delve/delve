package native

import (
	"bytes"
	"debug/elf"
	"fmt"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
	"golang.org/x/arch/riscv64/riscv64asm"
)

func (thread *nativeThread) fpRegisters() ([]proc.Register, []byte, error) {
	var err error
	var riscv64_fpregs linutil.RISCV64PtraceFpRegs

	thread.dbp.execPtraceFunc(func() { err = ptraceGetFpRegset(thread.ID, &riscv64_fpregs) })
	fpregs := riscv64_fpregs.Decode()

	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}

	return fpregs, riscv64_fpregs.Fregs, err
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	var restoreRegistersErr error

	sr := savedRegs.(*linutil.RISCV64Registers)
	t.dbp.execPtraceFunc(func() {
		restoreRegistersErr = ptraceSetGRegs(t.ID, sr.Regs)
		if restoreRegistersErr != syscall.Errno(0) {
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

// resolvePC is used to resolve next PC for current instruction.
func (t *nativeThread) resolvePC(savedRegs proc.Registers) ([]uint64, error) {
	regs := savedRegs.(*linutil.RISCV64Registers)
	nextInstLen := t.BinInfo().Arch.MaxInstructionLength()
	nextInstBytes := make([]byte, nextInstLen)
	var err error

	t.dbp.execPtraceFunc(func() {
		_, err = sys.PtracePeekData(t.ID, uintptr(regs.PC()), nextInstBytes)
	})
	if err != nil {
		return nil, err
	}

	nextPCs := []uint64{regs.PC() + uint64(nextInstLen)}
	if bytes.Equal(nextInstBytes, t.BinInfo().Arch.AltBreakpointInstruction()) {
		return nextPCs, nil
	} else if bytes.Equal(nextInstBytes, t.BinInfo().Arch.BreakpointInstruction()) {
		nextInstLen = 2
		nextPCs = []uint64{regs.PC() + uint64(nextInstLen)}
		return nextPCs, nil
	}

	nextInst, err := riscv64asm.Decode(nextInstBytes)
	if err != nil {
		return nil, err
	}

	if nextInst.Len == 2 {
		nextInstBytes = nextInstBytes[:2]
		nextInstLen = 2
		nextPCs = []uint64{regs.PC() + uint64(nextInstLen)}
	}

	switch nextInst.Op {
	case riscv64asm.BEQ, riscv64asm.BNE, riscv64asm.BLT, riscv64asm.BGE, riscv64asm.BLTU, riscv64asm.BGEU:
		rs1, _ := nextInst.Args[0].(riscv64asm.Reg)
		rs2, _ := nextInst.Args[1].(riscv64asm.Reg)
		bimm12, _ := nextInst.Args[2].(riscv64asm.Simm)
		src1u, _ := regs.GetReg(uint64(rs1))
		src2u, _ := regs.GetReg(uint64(rs2))
		src1, src2 := int64(src1u), int64(src2u)

		switch nextInst.Op {
		case riscv64asm.BEQ:
			if src1 == src2 && int(bimm12.Imm) != nextInstLen {
				nextPCs = append(nextPCs, regs.PC()+uint64(bimm12.Imm))
			}
		case riscv64asm.BNE:
			if src1 != src2 && int(bimm12.Imm) != nextInstLen {
				nextPCs = append(nextPCs, regs.PC()+uint64(bimm12.Imm))
			}
		case riscv64asm.BLT:
			if src1 < src2 && int(bimm12.Imm) != nextInstLen {
				nextPCs = append(nextPCs, regs.PC()+uint64(bimm12.Imm))
			}
		case riscv64asm.BGE:
			if src1 >= src2 && int(bimm12.Imm) != nextInstLen {
				nextPCs = append(nextPCs, regs.PC()+uint64(bimm12.Imm))
			}
		case riscv64asm.BLTU:
			if src1u < src2u && int(bimm12.Imm) != nextInstLen {
				nextPCs = append(nextPCs, regs.PC()+uint64(bimm12.Imm))
			}
		case riscv64asm.BGEU:
			if src1u >= src2u && int(bimm12.Imm) != nextInstLen {
				nextPCs = append(nextPCs, regs.PC()+uint64(bimm12.Imm))
			}
		}

	case riscv64asm.JAL:
		jimm, _ := nextInst.Args[1].(riscv64asm.Simm)
		if int(jimm.Imm) != nextInstLen {
			nextPCs = append(nextPCs, regs.PC()+uint64(jimm.Imm))
		}

	case riscv64asm.JALR:
		rd, _ := nextInst.Args[0].(riscv64asm.Reg)
		rs1_mem := nextInst.Args[1].(riscv64asm.RegOffset)
		rs1, ofs := rs1_mem.OfsReg, rs1_mem.Ofs
		src1, _ := regs.GetReg(uint64(rs1))
		if rd == riscv64asm.X0 && rs1 == riscv64asm.X1 {
			nextPCs = []uint64{(src1 + uint64(ofs.Imm)) & (^uint64(0x1))}
		}
		if (src1+uint64(ofs.Imm))&(^uint64(0x1)) != nextPCs[0] {
			nextPCs = append(nextPCs, (src1+uint64(ofs.Imm))&(^uint64(0x1)))
		}

	// We can't put a breakpoint in the middle of a lr/sc atomic sequence, so look for the end of the sequence and put the breakpoint there.
	// RISC-V Go only use lr.w/d.aq, see comments at the beginning of $GOROOT/src/runtime/internal/atomic/atomic_riscv64.s
	case riscv64asm.LR_D_AQ, riscv64asm.LR_W_AQ:
		// Currently, RISC-V Go only use this kind of lr/sc sequence, so only check this pattern.
		// defined in $GOROOT/src/cmd/compile/internal/riscv64/ssa.go:
		// LR	(Rarg0), Rtmp
		// BNE	Rtmp, Rarg1, 3(PC)
		// SC	Rarg2, (Rarg0), Rtmp
		// BNE	Rtmp, ZERO, -3(PC)
		curPC := regs.PC() + uint64(nextInstLen)
		t.dbp.execPtraceFunc(func() {
			_, err = sys.PtracePeekData(t.ID, uintptr(curPC), nextInstBytes)
		})
		if err != nil {
			return nil, err
		}
		nextInst, err = riscv64asm.Decode(nextInstBytes)
		if err != nil {
			return nil, err
		}
		if nextInst.Len == 2 {
			nextInstBytes = nextInstBytes[:2]
		}
		if nextInst.Op != riscv64asm.BNE {
			break
		}

		curPC += uint64(nextInstLen)
		t.dbp.execPtraceFunc(func() {
			_, err = sys.PtracePeekData(t.ID, uintptr(curPC), nextInstBytes)
		})
		if err != nil {
			return nil, err
		}
		nextInst, err = riscv64asm.Decode(nextInstBytes)
		if err != nil {
			return nil, err
		}
		if nextInst.Len == 2 {
			nextInstBytes = nextInstBytes[:2]
		}
		if nextInst.Op != riscv64asm.SC_D_RL && nextInst.Op != riscv64asm.SC_W_RL {
			break
		}

		curPC += uint64(nextInstLen)
		t.dbp.execPtraceFunc(func() {
			_, err = sys.PtracePeekData(t.ID, uintptr(regs.PC()+uint64(curPC)), nextInstBytes)
		})
		if err != nil {
			return nil, err
		}
		nextInst, err = riscv64asm.Decode(nextInstBytes)
		if err != nil {
			return nil, err
		}
		if nextInst.Len == 2 {
			nextInstBytes = nextInstBytes[:2]
		}
		if nextInst.Op != riscv64asm.BNE {
			break
		}
		nextPCs = []uint64{curPC}
	}

	return nextPCs, nil
}

// RISC-V doesn't have ptrace singlestep support, so use breakpoint to emulate it.
func (procgrp *processGroup) singleStep(t *nativeThread) (err error) {
	regs, err := t.Registers()
	if err != nil {
		return err
	}
	nextPCs, err := t.resolvePC(regs)
	if err != nil {
		return err
	}
	originalDataSet := make(map[uintptr][]byte)

	// Do in batch, first set breakpoint, then continue.
	t.dbp.execPtraceFunc(func() {
		breakpointInstr := t.BinInfo().Arch.BreakpointInstruction()
		readWriteMem := func(i int, addr uintptr, instr []byte) error {
			originalData := make([]byte, len(breakpointInstr))
			_, err = sys.PtracePeekData(t.ID, addr, originalData)
			if err != nil {
				return err
			}
			_, err = sys.PtracePokeData(t.ID, addr, instr)
			if err != nil {
				return err
			}
			// Everything is ok, store originalData
			originalDataSet[addr] = originalData
			return nil
		}
		for i, nextPC := range nextPCs {
			err = readWriteMem(i, uintptr(nextPC), breakpointInstr)
			if err != nil {
				return
			}
		}
	})
	// Make sure we restore before return.
	defer func() {
		t.dbp.execPtraceFunc(func() {
			for addr, originalData := range originalDataSet {
				if originalData != nil {
					_, err = sys.PtracePokeData(t.ID, addr, originalData)
				}
			}
		})
	}()
	if err != nil {
		return err
	}
	for {
		sig := 0
		t.dbp.execPtraceFunc(func() { err = ptraceCont(t.ID, sig) })
		if err != nil {
			return err
		}
		// To be able to catch process exit, we can only use wait instead of waitFast.
		wpid, status, err := t.dbp.wait(t.ID, 0)
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
		if wpid == t.ID {
			sig = 0
			switch s := status.StopSignal(); s {
			case sys.SIGTRAP:
				return nil
			case sys.SIGSTOP:
				// delayed SIGSTOP, ignore it
			case sys.SIGILL, sys.SIGBUS, sys.SIGFPE, sys.SIGSEGV, sys.SIGSTKFLT:
				// propagate signals that can have been caused by the current instruction
				sig = int(s)
			default:
				// delay propagation of all other signals
				t.os.delayedSignal = int(s)
			}
		}
	}
}
