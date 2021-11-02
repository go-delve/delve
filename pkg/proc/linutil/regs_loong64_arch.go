package linutil

import (
	"fmt"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
)

// Regs is a wrapper for sys.PtraceRegs.
type LOONG64Registers struct {
	Regs       *LOONG64PtraceRegs // general-purpose registers
	iscgo      bool
	tp_tls     uint64
	Fpregs     []proc.Register // Formatted floating point registers
	Fpregset   []byte          // holding all floating point register values
	loadFpRegs func(*LOONG64Registers) error
}

func NewLOONG64Registers(regs *LOONG64PtraceRegs, iscgo bool, tp_tls uint64,
	loadFpRegs func(*LOONG64Registers) error) *LOONG64Registers {
	return &LOONG64Registers{
		Regs:       regs,
		iscgo:      iscgo,
		tp_tls:     tp_tls,
		loadFpRegs: loadFpRegs,
	}
}

// LOONG64PtraceRegs is the struct used by the linux kernel to return the GPR
// for loongarch64 CPUs. refer to sys/unix/zptrace_linux_loong64.go
type LOONG64PtraceRegs struct {
	Regs [32]uint64
	Era  uint64
	BadV uint64
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *LOONG64Registers) Slice(floatingPoint bool) ([]proc.Register, error) {
	var regs64 = []struct {
		k string
		v uint64
	}{
		{"R0", r.Regs.Regs[regnum.LOONG64_R0]},
		{"R1", r.Regs.Regs[regnum.LOONG64_R1]},
		{"R2", r.Regs.Regs[regnum.LOONG64_R2]},
		{"R3", r.Regs.Regs[regnum.LOONG64_R3]},
		{"R4", r.Regs.Regs[regnum.LOONG64_R4]},
		{"R5", r.Regs.Regs[regnum.LOONG64_R5]},
		{"R6", r.Regs.Regs[regnum.LOONG64_R6]},
		{"R7", r.Regs.Regs[regnum.LOONG64_R7]},
		{"R8", r.Regs.Regs[regnum.LOONG64_R8]},
		{"R9", r.Regs.Regs[regnum.LOONG64_R9]},
		{"R10", r.Regs.Regs[regnum.LOONG64_R10]},
		{"R11", r.Regs.Regs[regnum.LOONG64_R11]},
		{"R12", r.Regs.Regs[regnum.LOONG64_R12]},
		{"R13", r.Regs.Regs[regnum.LOONG64_R13]},
		{"R14", r.Regs.Regs[regnum.LOONG64_R14]},
		{"R15", r.Regs.Regs[regnum.LOONG64_R15]},
		{"R16", r.Regs.Regs[regnum.LOONG64_R16]},
		{"R17", r.Regs.Regs[regnum.LOONG64_R17]},
		{"R18", r.Regs.Regs[regnum.LOONG64_R18]},
		{"R19", r.Regs.Regs[regnum.LOONG64_R19]},
		{"R20", r.Regs.Regs[regnum.LOONG64_R20]},
		{"R21", r.Regs.Regs[regnum.LOONG64_R21]},
		{"R22", r.Regs.Regs[regnum.LOONG64_R22]},
		{"R23", r.Regs.Regs[regnum.LOONG64_R23]},
		{"R24", r.Regs.Regs[regnum.LOONG64_R24]},
		{"R25", r.Regs.Regs[regnum.LOONG64_R25]},
		{"R26", r.Regs.Regs[regnum.LOONG64_R26]},
		{"R27", r.Regs.Regs[regnum.LOONG64_R27]},
		{"R28", r.Regs.Regs[regnum.LOONG64_R28]},
		{"R29", r.Regs.Regs[regnum.LOONG64_R29]},
		{"R30", r.Regs.Regs[regnum.LOONG64_R30]},
		{"R31", r.Regs.Regs[regnum.LOONG64_R31]},
		{"ERA", r.Regs.Era},
		{"BADV", r.Regs.BadV},
	}

	out := make([]proc.Register, 0, (len(regs64) + len(r.Fpregs)))
	for _, reg := range regs64 {
		out = proc.AppendUint64Register(out, reg.k, reg.v)
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

// PC returns the value of PC register.
func (r *LOONG64Registers) PC() uint64 {
	// PC Register
	return r.Regs.Era
}

// SP returns the value of SP register.
func (r *LOONG64Registers) SP() uint64 {
	// Stack pointer
	return r.Regs.Regs[regnum.LOONG64_SP]
}

// BP returns the value of FP register
func (r *LOONG64Registers) BP() uint64 {
	// Frame pointer
	return r.Regs.Regs[regnum.LOONG64_FP]
}

// TLS returns the address of the thread local storage memory segment.
func (r *LOONG64Registers) TLS() uint64 {
	// TODO: calling cgo may overwrite $r22,read it from the kernel
	if !r.iscgo {
		return 0
	}

	// refer to golang defined REGG: loong64/a.out.go
	return r.tp_tls
}

// GAddr returns the address of the G variable if it is known, 0 and false otherwise.
func (r *LOONG64Registers) GAddr() (uint64, bool) {
	// REGG is $r22,store the address of g
	return r.Regs.Regs[regnum.LOONG64_FP], !r.iscgo
}

// Copy returns a copy of these registers that is guaranteed not to change.
func (r *LOONG64Registers) Copy() (proc.Registers, error) {
	if r.loadFpRegs != nil {
		err := r.loadFpRegs(r)
		r.loadFpRegs = nil
		if err != nil {
			return nil, err
		}
	}

	var rr LOONG64Registers
	rr.Regs = &LOONG64PtraceRegs{}
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

type LOONG64PtraceFpRegs struct {
	Vregs []byte
	Fcc   uint64
	Fcsr  uint32
}

const _LOONG64_FP_VREGS_LENGTH = (32 * 8)

func (fpregs *LOONG64PtraceFpRegs) Decode() (regs []proc.Register) {
	// floating-pointer register : $f0 - $f32, 64-bit
	for i := 0; i < _LOONG64_FP_VREGS_LENGTH; i += 8 {
		name := fmt.Sprintf("$f%d", (i / 8))
		value := fpregs.Vregs[i : i+8]
		regs = proc.AppendBytesRegister(regs, name, value)
	}

	return
}

func (fpregs *LOONG64PtraceFpRegs) Byte() []byte {
	fpregs.Vregs = make([]byte, _LOONG64_FP_VREGS_LENGTH)

	return fpregs.Vregs[:]
}
