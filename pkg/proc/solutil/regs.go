package solutil

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/go-delve/delve/pkg/proc"
)

type AMD64Registers struct {
	Regs     AMD64Regset
	Fpregs   []proc.Register
	Fpregset *AMD64Fpregset

	loadFpRegs func(*AMD64Registers) error
}

func NewAMD64Registers(Regs AMD64Regset, loadFpRegs func(*AMD64Registers) error) *AMD64Registers {
	return &AMD64Registers{Regs: Regs, loadFpRegs: loadFpRegs}
}

// AMD64Regset represents CPU registers on an AMD64 processor.
// This order is determined by the defines in /usr/include/sys/regset.h.
type AMD64Regset struct {
	R15    int64
	R14    int64
	R13    int64
	R12    int64
	R11    int64
	R10    int64
	R9     int64
	R8     int64
	Rdi    int64
	Rsi    int64
	Rbp    int64
	Rbx    int64
	Rdx    int64
	Rcx    int64
	Rax    int64
	Trapno int64
	Err    int64
	Rip    int64
	Cs     int64
	Rflags int64
	Rsp    int64
	Ss     int64
	Fs     int64
	Gs     int64
	Es     int64
	Ds     int64
	FsBase int64
	GsBase int64
}

// AMD64FpRegset tracks fpregset_t in /usr/include/sys/mcontext.h.
type AMD64Fpregset struct {
	Cw        uint16
	Sw        uint16
	Fctw      uint8
	fxRsvd    uint8
	Fop       uint16
	Rip       uint64
	Rdp       uint64
	Mxcsr     uint32
	MxcsrMask uint32
	St        [32]uint32
	Xmm       [256]byte
	fxIgn2    [24]uint32
	Status    uint32
	Xstatus   uint32
}

// Decode decodes fpregset_t to a list of name/value pairs of registers.
func (r *AMD64Fpregset) Decode() []proc.Register {
	var regs []proc.Register
	// x87 registers
	regs = proc.AppendUint64Register(regs, "CW", uint64(r.Cw))
	regs = proc.AppendUint64Register(regs, "SW", uint64(r.Sw))
	regs = proc.AppendUint64Register(regs, "FCTW", uint64(r.Fctw))
	regs = proc.AppendUint64Register(regs, "FOP", uint64(r.Fop))
	regs = proc.AppendUint64Register(regs, "FIP", r.Rip)
	regs = proc.AppendUint64Register(regs, "FDP", r.Rdp)

	for i := 0; i < len(r.St); i += 4 {
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, uint64(r.St[i+1])<<32|uint64(r.St[i]))
		binary.Write(&buf, binary.LittleEndian, uint16(r.St[i+2]))
		regs = proc.AppendBytesRegister(regs, fmt.Sprintf("ST(%d)", i/4), buf.Bytes())
	}

	// SSE registers
	regs = proc.AppendUint64Register(regs, "MXCSR", uint64(r.Mxcsr))
	regs = proc.AppendUint64Register(regs, "MXCSR_MASK", uint64(r.MxcsrMask))

	for i := 0; i < len(r.Xmm); i += 16 {
		n := i / 16
		regs = proc.AppendBytesRegister(regs, fmt.Sprintf("XMM%d", n), r.Xmm[i:i+16])
	}

	return regs
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *AMD64Registers) Slice(floatingPoint bool) ([]proc.Register, error) {
	var regs = [28]struct {
		k string
		v int64
	}{
		{"R15", r.Regs.R15},
		{"R14", r.Regs.R14},
		{"R13", r.Regs.R13},
		{"R12", r.Regs.R12},
		{"R11", r.Regs.R11},
		{"R10", r.Regs.R10},
		{"R9", r.Regs.R9},
		{"R8", r.Regs.R8},
		{"Rdi", r.Regs.Rdi},
		{"Rsi", r.Regs.Rsi},
		{"Rbp", r.Regs.Rbp},
		{"Rbx", r.Regs.Rbx},
		{"Rdx", r.Regs.Rdx},
		{"Rcx", r.Regs.Rcx},
		{"Rax", r.Regs.Rax},
		{"Trapno", r.Regs.Trapno},
		{"Err", r.Regs.Err},
		{"Rip", r.Regs.Rip},
		{"Cs", r.Regs.Cs},
		{"Rflags", r.Regs.Rflags},
		{"Rsp", r.Regs.Rsp},
		{"Ss", r.Regs.Ss},
		{"Fs", r.Regs.Fs},
		{"Gs", r.Regs.Gs},
		{"Es", r.Regs.Es},
		{"Ds", r.Regs.Ds},
		{"Fs_base", r.Regs.FsBase},
		{"Gs_base", r.Regs.GsBase},
	}
	out := make([]proc.Register, 0, len(regs))
	for _, reg := range regs {
		// Solaris defines the registers as signed, but Linux defines
		// them as unsigned.  Of course, a register doesn't really have
		// a concept of signedness.  Cast to what Delve expects.
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

// PC returns the current program counter
// i.e. the RIP CPU register.
func (r *AMD64Registers) PC() uint64 {
	return uint64(r.Regs.Rip)
}

// SP returns the stack pointer location,
// i.e. the RSP register.
func (r *AMD64Registers) SP() uint64 {
	return uint64(r.Regs.Rsp)
}

func (r *AMD64Registers) BP() uint64 {
	return uint64(r.Regs.Rbp)
}

// TLS returns the value of the register
// that contains the location of the thread
// local storage segment.
func (r *AMD64Registers) TLS() uint64 {
	return uint64(r.Regs.FsBase)
}

func (r *AMD64Registers) GAddr() (uint64, bool) {
	return 0, false
}

// Copy returns a copy of these registers that is guaranteed not to change.
func (r *AMD64Registers) Copy() (proc.Registers, error) {
	if r.loadFpRegs != nil {
		err := r.loadFpRegs(r)
		r.loadFpRegs = nil
		if err != nil {
			return nil, err
		}
	}
	var rr AMD64Registers
	rr.Regs = r.Regs
	rr.Fpregset = r.Fpregset
	if r.Fpregs != nil {
		rr.Fpregs = make([]proc.Register, len(r.Fpregs))
		copy(rr.Fpregs, r.Fpregs)
	}
	return &rr, nil
}
