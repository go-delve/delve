package winutil

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/arch/x86/x86asm"

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
	Context *CONTEXT
	fltSave *XMM_SAVE_AREA32
}

// NewAMD64Registers creates a new AMD64Registers struct from a CONTEXT
// struct and the TEB base address of the thread.
func NewAMD64Registers(context *CONTEXT, TebBaseAddress uint64) *AMD64Registers {
	regs := &AMD64Registers{
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

// Get returns the value of the n-th register (in x86asm order).
func (r *AMD64Registers) Get(n int) (uint64, error) {
	reg := x86asm.Reg(n)
	const (
		mask8  = 0x000f
		mask16 = 0x00ff
		mask32 = 0xffff
	)

	switch reg {
	// 8-bit
	case x86asm.AL:
		return r.rax & mask8, nil
	case x86asm.CL:
		return r.rcx & mask8, nil
	case x86asm.DL:
		return r.rdx & mask8, nil
	case x86asm.BL:
		return r.rbx & mask8, nil
	case x86asm.AH:
		return (r.rax >> 8) & mask8, nil
	case x86asm.CH:
		return (r.rcx >> 8) & mask8, nil
	case x86asm.DH:
		return (r.rdx >> 8) & mask8, nil
	case x86asm.BH:
		return (r.rbx >> 8) & mask8, nil
	case x86asm.SPB:
		return r.rsp & mask8, nil
	case x86asm.BPB:
		return r.rbp & mask8, nil
	case x86asm.SIB:
		return r.rsi & mask8, nil
	case x86asm.DIB:
		return r.rdi & mask8, nil
	case x86asm.R8B:
		return r.r8 & mask8, nil
	case x86asm.R9B:
		return r.r9 & mask8, nil
	case x86asm.R10B:
		return r.r10 & mask8, nil
	case x86asm.R11B:
		return r.r11 & mask8, nil
	case x86asm.R12B:
		return r.r12 & mask8, nil
	case x86asm.R13B:
		return r.r13 & mask8, nil
	case x86asm.R14B:
		return r.r14 & mask8, nil
	case x86asm.R15B:
		return r.r15 & mask8, nil

	// 16-bit
	case x86asm.AX:
		return r.rax & mask16, nil
	case x86asm.CX:
		return r.rcx & mask16, nil
	case x86asm.DX:
		return r.rdx & mask16, nil
	case x86asm.BX:
		return r.rbx & mask16, nil
	case x86asm.SP:
		return r.rsp & mask16, nil
	case x86asm.BP:
		return r.rbp & mask16, nil
	case x86asm.SI:
		return r.rsi & mask16, nil
	case x86asm.DI:
		return r.rdi & mask16, nil
	case x86asm.R8W:
		return r.r8 & mask16, nil
	case x86asm.R9W:
		return r.r9 & mask16, nil
	case x86asm.R10W:
		return r.r10 & mask16, nil
	case x86asm.R11W:
		return r.r11 & mask16, nil
	case x86asm.R12W:
		return r.r12 & mask16, nil
	case x86asm.R13W:
		return r.r13 & mask16, nil
	case x86asm.R14W:
		return r.r14 & mask16, nil
	case x86asm.R15W:
		return r.r15 & mask16, nil

	// 32-bit
	case x86asm.EAX:
		return r.rax & mask32, nil
	case x86asm.ECX:
		return r.rcx & mask32, nil
	case x86asm.EDX:
		return r.rdx & mask32, nil
	case x86asm.EBX:
		return r.rbx & mask32, nil
	case x86asm.ESP:
		return r.rsp & mask32, nil
	case x86asm.EBP:
		return r.rbp & mask32, nil
	case x86asm.ESI:
		return r.rsi & mask32, nil
	case x86asm.EDI:
		return r.rdi & mask32, nil
	case x86asm.R8L:
		return r.r8 & mask32, nil
	case x86asm.R9L:
		return r.r9 & mask32, nil
	case x86asm.R10L:
		return r.r10 & mask32, nil
	case x86asm.R11L:
		return r.r11 & mask32, nil
	case x86asm.R12L:
		return r.r12 & mask32, nil
	case x86asm.R13L:
		return r.r13 & mask32, nil
	case x86asm.R14L:
		return r.r14 & mask32, nil
	case x86asm.R15L:
		return r.r15 & mask32, nil

	// 64-bit
	case x86asm.RAX:
		return r.rax, nil
	case x86asm.RCX:
		return r.rcx, nil
	case x86asm.RDX:
		return r.rdx, nil
	case x86asm.RBX:
		return r.rbx, nil
	case x86asm.RSP:
		return r.rsp, nil
	case x86asm.RBP:
		return r.rbp, nil
	case x86asm.RSI:
		return r.rsi, nil
	case x86asm.RDI:
		return r.rdi, nil
	case x86asm.R8:
		return r.r8, nil
	case x86asm.R9:
		return r.r9, nil
	case x86asm.R10:
		return r.r10, nil
	case x86asm.R11:
		return r.r11, nil
	case x86asm.R12:
		return r.r12, nil
	case x86asm.R13:
		return r.r13, nil
	case x86asm.R14:
		return r.r14, nil
	case x86asm.R15:
		return r.r15, nil
	}

	return 0, proc.ErrUnknownRegister
}

// Copy returns a copy of these registers that is guaranteed not to change.
func (r *AMD64Registers) Copy() (proc.Registers, error) {
	var rr AMD64Registers
	rr = *r
	rr.Context = NewCONTEXT()
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

// CONTEXT tracks the _CONTEXT of windows.
type CONTEXT struct {
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

// NewCONTEXT allocates Windows CONTEXT structure aligned to 16 bytes.
func NewCONTEXT() *CONTEXT {
	var c *CONTEXT
	buf := make([]byte, unsafe.Sizeof(*c)+15)
	return (*CONTEXT)(unsafe.Pointer((uintptr(unsafe.Pointer(&buf[15]))) &^ 15))
}
