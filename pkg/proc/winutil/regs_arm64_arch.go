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

const (
	ARM64_MAX_BREAKPOINTS = 8
	ARM64_MAX_WATCHPOINTS = 2
)

// neon128 tracks the neon128 windows struct.
type neon128 struct {
	Low  uint64
	High int64
}

// ARM64Registers represents CPU registers on an ARM64 processor.
type ARM64Registers struct {
	iscgo          bool
	Regs           [31]uint64
	Sp             uint64
	Pc             uint64
	FloatRegisters [32]neon128
	Fpcr           uint32
	Fpsr           uint32
	Bcr            [ARM64_MAX_BREAKPOINTS]uint32
	Bvr            [ARM64_MAX_BREAKPOINTS]uint64
	Wcr            [ARM64_MAX_WATCHPOINTS]uint32
	Wvr            [ARM64_MAX_WATCHPOINTS]uint64
	Context        *ARM64CONTEXT
}

// NewARM64Registers creates a new ARM64Registers struct from a CONTEXT.
func NewARM64Registers(context *ARM64CONTEXT, iscgo bool) *ARM64Registers {
	regs := &ARM64Registers{
		iscgo:          iscgo,
		Regs:           context.Regs,
		Sp:             context.Sp,
		Pc:             context.Pc,
		FloatRegisters: context.FloatRegisters,
		Fpcr:           context.Fpcr,
		Fpsr:           context.Fpsr,
		Bcr:            context.Bcr,
		Bvr:            context.Bvr,
		Wcr:            context.Wcr,
		Wvr:            context.Wvr,
		Context:        context,
	}

	return regs
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *ARM64Registers) Slice(floatingPoint bool) ([]proc.Register, error) {
	out := make([]proc.Register, 0, len(r.Regs)+len(r.FloatRegisters)+2)
	for i, v := range r.Regs {
		out = proc.AppendUint64Register(out, fmt.Sprintf("X%d", i), v)
	}
	out = proc.AppendUint64Register(out, "SP", r.Sp)
	out = proc.AppendUint64Register(out, "PC", r.Pc)
	if floatingPoint {
		for i := range r.FloatRegisters {
			var buf bytes.Buffer
			binary.Write(&buf, binary.LittleEndian, r.FloatRegisters[i].Low)
			binary.Write(&buf, binary.LittleEndian, r.FloatRegisters[i].High)
			out = proc.AppendBytesRegister(out, fmt.Sprintf("V%d", i), buf.Bytes())
		}
		out = proc.AppendUint64Register(out, "Fpcr", uint64(r.Fpcr))
		out = proc.AppendUint64Register(out, "Fpsr", uint64(r.Fpsr))
	}
	return out, nil
}

// PC returns the value of RIP register.
func (r *ARM64Registers) PC() uint64 {
	return r.Pc
}

// SP returns the value of RSP register.
func (r *ARM64Registers) SP() uint64 {
	return r.Sp
}

func (r *ARM64Registers) BP() uint64 {
	return r.Regs[29]
}

// TLS returns the address of the thread local storage memory segment.
func (r *ARM64Registers) TLS() uint64 {
	if !r.iscgo {
		return 0
	}
	return r.Regs[18]
}

// GAddr returns the address of the G variable if it is known, 0 and false
// otherwise.
func (r *ARM64Registers) GAddr() (uint64, bool) {
	return r.Regs[28], !r.iscgo
}

// LR returns the link register.
func (r *ARM64Registers) LR() uint64 {
	return r.Regs[30]
}

// Copy returns a copy of these registers that is guaranteed not to change.
func (r *ARM64Registers) Copy() (proc.Registers, error) {
	rr := *r
	rr.Context = NewARM64CONTEXT()
	*(rr.Context) = *(r.Context)
	return &rr, nil
}

// ARM64CONTEXT tracks the _ARM64_NT_CONTEXT of windows.
type ARM64CONTEXT struct {
	ContextFlags   uint32
	Cpsr           uint32
	Regs           [31]uint64
	Sp             uint64
	Pc             uint64
	FloatRegisters [32]neon128
	Fpcr           uint32
	Fpsr           uint32
	Bcr            [ARM64_MAX_BREAKPOINTS]uint32
	Bvr            [ARM64_MAX_BREAKPOINTS]uint64
	Wcr            [ARM64_MAX_WATCHPOINTS]uint32
	Wvr            [ARM64_MAX_WATCHPOINTS]uint64
}

// NewARM64CONTEXT allocates Windows CONTEXT structure aligned to 16 bytes.
func NewARM64CONTEXT() *ARM64CONTEXT {
	var c *ARM64CONTEXT
	buf := make([]byte, unsafe.Sizeof(*c)+15)
	return (*ARM64CONTEXT)(unsafe.Pointer((uintptr(unsafe.Pointer(&buf[15]))) &^ 15))
}

func (ctx *ARM64CONTEXT) SetFlags(flags uint32) {
	ctx.ContextFlags = flags
}

func (ctx *ARM64CONTEXT) SetPC(pc uint64) {
	ctx.Pc = pc
}

func (ctx *ARM64CONTEXT) SetTrap(trap bool) {
	const v = 0x200000
	if trap {
		ctx.Cpsr |= v
	} else {
		ctx.Cpsr &= ^uint32(v)
	}
}

func (ctx *ARM64CONTEXT) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	switch regNum {
	case regnum.ARM64_PC:
		ctx.Pc = reg.Uint64Val
		return nil
	case regnum.ARM64_SP:
		ctx.Sp = reg.Uint64Val
		return nil
	default:
		switch {
		case regNum >= regnum.ARM64_X0 && regNum <= regnum.ARM64_X0+30:
			ctx.Regs[regNum-regnum.ARM64_X0] = reg.Uint64Val
			return nil

		case regNum >= regnum.ARM64_V0 && regNum <= regnum.ARM64_V0+30:
			i := regNum - regnum.ARM64_V0
			ctx.FloatRegisters[i].Low = reg.Uint64Val
			ctx.FloatRegisters[i].High = 0
			return nil
		default:
			return fmt.Errorf("changing register %d not implemented", regNum)
		}
	}
}
