package winutil

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
)

// AMD64Registers represents CPU registers on an AMD64 processor.
type AMD64Registers struct {
	rax     uint64
	rbx     uint64
	rcx     uint64
	rdx     uint64
	rdi     uint64
	rsi     uint64
	rbp     uint64
	rsp     uint64
	r8      uint64
	r9      uint64
	r10     uint64
	r11     uint64
	r12     uint64
	r13     uint64
	r14     uint64
	r15     uint64
	rip     uint64
	eflags  uint64
	cs      uint64
	fs      uint64
	gs      uint64
	tls     uint64
	Context *AMD64CONTEXT
	fltSave *XMM_SAVE_AREA32
}

// NewAMD64Registers creates a new AMD64Registers struct from a CONTEXT
// struct and the TEB base address of the thread.
func NewAMD64Registers(context *AMD64CONTEXT, TebBaseAddress uint64) *AMD64Registers {
	regs := &AMD64Registers{
		rax:    context.Rax,
		rbx:    context.Rbx,
		rcx:    context.Rcx,
		rdx:    context.Rdx,
		rdi:    context.Rdi,
		rsi:    context.Rsi,
		rbp:    context.Rbp,
		rsp:    context.Rsp,
		r8:     context.R8,
		r9:     context.R9,
		r10:    context.R10,
		r11:    context.R11,
		r12:    context.R12,
		r13:    context.R13,
		r14:    context.R14,
		r15:    context.R15,
		rip:    context.Rip,
		eflags: uint64(context.EFlags),
		cs:     uint64(context.SegCs),
		fs:     uint64(context.SegFs),
		gs:     uint64(context.SegGs),
		tls:    TebBaseAddress,
	}

	regs.fltSave = &context.FltSave
	regs.Context = context

	return regs
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *AMD64Registers) Slice(floatingPoint bool) ([]proc.Register, error) {
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
		{"Rflags", r.eflags},
		{"Cs", r.cs},
		{"Fs", r.fs},
		{"Gs", r.gs},
		{"TLS", r.tls},
	}
	outlen := len(regs)
	if r.fltSave != nil && floatingPoint {
		outlen += 6 + 8 + 2 + 16
	}
	out := make([]proc.Register, 0, outlen)
	for _, reg := range regs {
		out = proc.AppendUint64Register(out, reg.k, reg.v)
	}
	if r.fltSave != nil && floatingPoint {
		out = proc.AppendUint64Register(out, "CW", uint64(r.fltSave.ControlWord))
		out = proc.AppendUint64Register(out, "SW", uint64(r.fltSave.StatusWord))
		out = proc.AppendUint64Register(out, "TW", uint64(uint16(r.fltSave.TagWord)))
		out = proc.AppendUint64Register(out, "FOP", uint64(r.fltSave.ErrorOpcode))
		out = proc.AppendUint64Register(out, "FIP", uint64(r.fltSave.ErrorSelector)<<32|uint64(r.fltSave.ErrorOffset))
		out = proc.AppendUint64Register(out, "FDP", uint64(r.fltSave.DataSelector)<<32|uint64(r.fltSave.DataOffset))

		for i := range r.fltSave.FloatRegisters {
			var buf bytes.Buffer
			binary.Write(&buf, binary.LittleEndian, r.fltSave.FloatRegisters[i].Low)
			binary.Write(&buf, binary.LittleEndian, r.fltSave.FloatRegisters[i].High)
			out = proc.AppendBytesRegister(out, fmt.Sprintf("ST(%d)", i), buf.Bytes())
		}

		out = proc.AppendUint64Register(out, "MXCSR", uint64(r.fltSave.MxCsr))
		out = proc.AppendUint64Register(out, "MXCSR_MASK", uint64(r.fltSave.MxCsr_Mask))

		for i := 0; i < len(r.fltSave.XmmRegisters); i += 16 {
			out = proc.AppendBytesRegister(out, fmt.Sprintf("XMM%d", i/16), r.fltSave.XmmRegisters[i:i+16])
		}
	}
	return out, nil
}

// PC returns the current program counter
// i.e. the RIP CPU register.
func (r *AMD64Registers) PC() uint64 {
	return r.rip
}

// SP returns the stack pointer location,
// i.e. the RSP register.
func (r *AMD64Registers) SP() uint64 {
	return r.rsp
}

func (r *AMD64Registers) BP() uint64 {
	return r.rbp
}

// LR returns the link register.
func (r *AMD64Registers) LR() uint64 {
	return 0
}

// TLS returns the value of the register
// that contains the location of the thread
// local storage segment.
func (r *AMD64Registers) TLS() uint64 {
	return r.tls
}

// GAddr returns the address of the G variable if it is known, 0 and false
// otherwise.
func (r *AMD64Registers) GAddr() (uint64, bool) {
	return 0, false
}

// Copy returns a copy of these registers that is guaranteed not to change.
func (r *AMD64Registers) Copy() (proc.Registers, error) {
	var rr AMD64Registers
	rr = *r
	rr.Context = NewAMD64CONTEXT()
	*(rr.Context) = *(r.Context)
	rr.fltSave = &rr.Context.FltSave
	return &rr, nil
}

// M128A tracks the _M128A windows struct.
type M128A struct {
	Low  uint64
	High int64
}

// XMM_SAVE_AREA32 tracks the _XMM_SAVE_AREA32 windows struct.
type XMM_SAVE_AREA32 struct {
	ControlWord    uint16
	StatusWord     uint16
	TagWord        byte
	Reserved1      byte
	ErrorOpcode    uint16
	ErrorOffset    uint32
	ErrorSelector  uint16
	Reserved2      uint16
	DataOffset     uint32
	DataSelector   uint16
	Reserved3      uint16
	MxCsr          uint32
	MxCsr_Mask     uint32
	FloatRegisters [8]M128A
	XmmRegisters   [256]byte
	Reserved4      [96]byte
}

// AMD64CONTEXT tracks the _CONTEXT of windows.
type AMD64CONTEXT struct {
	P1Home uint64
	P2Home uint64
	P3Home uint64
	P4Home uint64
	P5Home uint64
	P6Home uint64

	ContextFlags uint32
	MxCsr        uint32

	SegCs  uint16
	SegDs  uint16
	SegEs  uint16
	SegFs  uint16
	SegGs  uint16
	SegSs  uint16
	EFlags uint32

	Dr0 uint64
	Dr1 uint64
	Dr2 uint64
	Dr3 uint64
	Dr6 uint64
	Dr7 uint64

	Rax uint64
	Rcx uint64
	Rdx uint64
	Rbx uint64
	Rsp uint64
	Rbp uint64
	Rsi uint64
	Rdi uint64
	R8  uint64
	R9  uint64
	R10 uint64
	R11 uint64
	R12 uint64
	R13 uint64
	R14 uint64
	R15 uint64

	Rip uint64

	FltSave XMM_SAVE_AREA32

	VectorRegister [26]M128A
	VectorControl  uint64

	DebugControl         uint64
	LastBranchToRip      uint64
	LastBranchFromRip    uint64
	LastExceptionToRip   uint64
	LastExceptionFromRip uint64
}

// NewAMD64CONTEXT allocates Windows CONTEXT structure aligned to 16 bytes.
func NewAMD64CONTEXT() *AMD64CONTEXT {
	var c *AMD64CONTEXT
	buf := make([]byte, unsafe.Sizeof(*c)+15)
	return (*AMD64CONTEXT)(unsafe.Pointer((uintptr(unsafe.Pointer(&buf[15]))) &^ 15))
}

func (ctx *AMD64CONTEXT) SetFlags(flags uint32) {
	ctx.ContextFlags = flags
}

func (ctx *AMD64CONTEXT) SetPC(pc uint64) {
	ctx.Rip = pc
}

func (ctx *AMD64CONTEXT) SetTrap(trap bool) {
	const v = 0x100
	if trap {
		ctx.EFlags |= v
	} else {
		ctx.EFlags &= ^uint32(v)
	}
}

func (ctx *AMD64CONTEXT) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	var p *uint64

	switch regNum {
	case regnum.AMD64_Rax:
		p = &ctx.Rax
	case regnum.AMD64_Rbx:
		p = &ctx.Rbx
	case regnum.AMD64_Rcx:
		p = &ctx.Rcx
	case regnum.AMD64_Rdx:
		p = &ctx.Rdx
	case regnum.AMD64_Rsi:
		p = &ctx.Rsi
	case regnum.AMD64_Rdi:
		p = &ctx.Rdi
	case regnum.AMD64_Rbp:
		p = &ctx.Rbp
	case regnum.AMD64_Rsp:
		p = &ctx.Rsp
	case regnum.AMD64_R8:
		p = &ctx.R8
	case regnum.AMD64_R9:
		p = &ctx.R9
	case regnum.AMD64_R10:
		p = &ctx.R10
	case regnum.AMD64_R11:
		p = &ctx.R11
	case regnum.AMD64_R12:
		p = &ctx.R12
	case regnum.AMD64_R13:
		p = &ctx.R13
	case regnum.AMD64_R14:
		p = &ctx.R14
	case regnum.AMD64_R15:
		p = &ctx.R15
	case regnum.AMD64_Rip:
		p = &ctx.Rip
	}

	if p != nil {
		if reg.Bytes != nil && len(reg.Bytes) != 8 {
			return fmt.Errorf("wrong number of bytes for register %s (%d)", regnum.AMD64ToName(regNum), len(reg.Bytes))
		}
		*p = reg.Uint64Val
	} else if regNum == regnum.AMD64_Rflags {
		ctx.EFlags = uint32(reg.Uint64Val)
	} else {
		if regNum < regnum.AMD64_XMM0 || regNum > regnum.AMD64_XMM0+15 {
			return fmt.Errorf("can not set register %s", regnum.AMD64ToName(regNum))
		}
		reg.FillBytes()
		if len(reg.Bytes) > 16 {
			return fmt.Errorf("too many bytes when setting register %s", regnum.AMD64ToName(regNum))
		}
		copy(ctx.FltSave.XmmRegisters[(regNum-regnum.AMD64_XMM0)*16:], reg.Bytes)
	}
	return nil
}
