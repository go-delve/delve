package linutil

import (
	"fmt"
	"github.com/go-delve/delve/pkg/proc"
	"golang.org/x/arch/arm/armasm"
)

// Regs is a wrapper for sys.PtraceRegs.
type ARMRegisters struct {
	Regs     *ARMPtraceRegs  //general-purpose registers
	Fpregs   []proc.Register //Formatted floating point registers
	Fpregset []byte          //holding all floating point register values

	loadFpRegs func(*ARMRegisters) error
}

func NewARMRegisters(regs *ARMPtraceRegs, loadFpRegs func(*ARMRegisters) error) *ARMRegisters {
	return &ARMRegisters{Regs: regs, loadFpRegs: loadFpRegs}
}

// ARMPtraceRegs is the struct used by the linux kernel to return the
// general purpose registers for ARM CPUs.
// copy from sys/unix/ztypes_linux_arm.go:737
type ARMPtraceRegs struct {
	Uregs [18]uint32
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *ARMRegisters) Slice(floatingPoint bool) ([]proc.Register, error) {
	var regs = []struct {
		k string
		v uint32
	}{
		{"R0", r.Regs.Uregs[0]},
		{"R1", r.Regs.Uregs[1]},
		{"R2", r.Regs.Uregs[2]},
		{"R3", r.Regs.Uregs[3]},
		{"R4", r.Regs.Uregs[4]},
		{"R5", r.Regs.Uregs[5]},
		{"R6", r.Regs.Uregs[6]},
		{"R7", r.Regs.Uregs[7]},
		{"R8", r.Regs.Uregs[8]},
		{"R9", r.Regs.Uregs[9]},
		{"R10", r.Regs.Uregs[10]},
		{"FP", r.Regs.Uregs[11]},
		{"IP", r.Regs.Uregs[12]},
		{"SP", r.Regs.Uregs[13]},
		{"LR", r.Regs.Uregs[14]},
		{"PC", r.Regs.Uregs[15]},
		{"CPSR", r.Regs.Uregs[16]},
		{"ORIG_R0", r.Regs.Uregs[17]},
	}
	out := make([]proc.Register, 0, len(regs)+len(r.Fpregs))
	for _, reg := range regs {
		out = proc.AppendUint64Register(out, reg.k, uint64(reg.v))
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
func (r *ARMRegisters) PC() uint64 {
	return uint64(r.Regs.Uregs[15])
}

// SP returns the value of RSP register.
func (r *ARMRegisters) SP() uint64 {
	return uint64(r.Regs.Uregs[13])
}

func (r *ARMRegisters) BP() uint64 {
	return uint64(r.Regs.Uregs[11])
}

// TLS returns the address of the thread local storage memory segment.
func (r *ARMRegisters) TLS() uint64 {
	return 0
}

// GAddr returns the address of the G variable if it is known, 0 and false
// otherwise.
func (r *ARMRegisters) GAddr() (uint64, bool) {
	return uint64(r.Regs.Uregs[10]), true
}

// Get returns the value of the n-th register (in arm64asm order).
func (r *ARMRegisters) Get(n int) (uint64, error) {
	reg := armasm.Reg(n)

	if reg >= armasm.R0 && reg <= armasm.R15 {
		return uint64(r.Regs.Uregs[reg-armasm.R0]), nil
	}

	return 0, proc.ErrUnknownRegister
}

// Copy returns a copy of these registers that is guarenteed not to change.
func (r *ARMRegisters) Copy() (proc.Registers, error) {
	if r.loadFpRegs != nil {
		err := r.loadFpRegs(r)
		r.loadFpRegs = nil
		if err != nil {
			return nil, err
		}
	}
	var rr ARMRegisters
	rr.Regs = &ARMPtraceRegs{}
	*(rr.Regs) = *(r.Regs)
	if r.Fpregs != nil {
		rr.Fpregs = make([]proc.Register, len(r.Fpregs))
		copy(rr.Fpregs, r.Fpregs)
	}
	if r.Fpregset != nil {
		rr.Fpregset = make([]byte, len(r.Fpregset))
		copy(rr.Fpregset, r.Fpregset)
	}
	return &rr, nil
}

type ARMPtraceFpRegs struct {
	Vregs []byte
	Fpscr uint32
}

const _ARM32_FP_REGS_LENGTH = 32

func (fpregs *ARMPtraceFpRegs) Decode() (regs []proc.Register) {
	// According to arch/arm/include/asm/ptrace.h, the length of fpregs is 8.
	for i := 0; i < len(fpregs.Vregs); i += 8 {
		regs = proc.AppendBytesRegister(regs, fmt.Sprintf("D%d", i/8), fpregs.Vregs[i:i+8])
	}
	return
}

func (fpregs *ARMPtraceFpRegs) Byte() []byte {
	fpregs.Vregs = make([]byte, _ARM32_FP_REGS_LENGTH)
	return fpregs.Vregs[:]
}
