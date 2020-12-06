//+build darwin,arm64,macnative

package native

// #include "threads_darwin.h"
import "C"
import (
	"errors"
	"fmt"

	"github.com/go-delve/delve/pkg/proc"
	"golang.org/x/arch/arm64/arm64asm"
)

// Regs represents CPU registers on an ARM64 processor.
type Regs struct {
	x0     uint64
	x1     uint64
	x2     uint64
	x3     uint64
	x4     uint64
	x5     uint64
	x6     uint64
	x7     uint64
	x8     uint64
	x9     uint64
	x10    uint64
	x11    uint64
	x12    uint64
	x13    uint64
	x14    uint64
	x15    uint64
	x16    uint64
	x17    uint64
	x18    uint64
	x19    uint64
	x20    uint64
	x21    uint64
	x22    uint64
	x23    uint64
	x24    uint64
	x25    uint64
	x26    uint64
	x27    uint64
	x28    uint64
	fp     uint64
	lr     uint64
	sp     uint64
	pc     uint64
	cpsr   uint32
	fpregs []proc.Register
}

func (r *Regs) Slice(floatingPoint bool) ([]proc.Register, error) {
	var regs = []struct {
		k string
		v uint64
	}{
		{"X0", r.x0},
		{"X1", r.x0},
		{"X2", r.x2},
		{"X3", r.x3},
		{"X4", r.x4},
		{"X5", r.x5},
		{"X6", r.x6},
		{"X7", r.x7},
		{"X8", r.x8},
		{"X9", r.x9},
		{"X10", r.x10},
		{"X11", r.x11},
		{"X12", r.x12},
		{"X13", r.x13},
		{"X14", r.x14},
		{"X15", r.x15},
		{"X16", r.x16},
		{"X17", r.x17},
		{"X18", r.x18},
		{"X19", r.x19},
		{"X20", r.x20},
		{"X21", r.x21},
		{"X22", r.x22},
		{"X23", r.x23},
		{"X24", r.x24},
		{"X25", r.x25},
		{"X26", r.x26},
		{"X27", r.x27},
		{"X28", r.x28},
		{"FP", r.fp},
		{"LR", r.lr},
		{"SP", r.sp},
		{"PC", r.pc},
	}

	out := make([]proc.Register, 0, len(regs)+len(r.fpregs))
	/*for _, reg := range regs {
		if reg.k == "Rflags" {
			out = proc.AppendUint64Register(out, reg.k, reg.v)
		} else {
			out = proc.AppendUint64Register(out, reg.k, reg.v)
		}
	}*/
	if floatingPoint {
		out = append(out, r.fpregs...)
	}

	return out, nil
}

// PC returns the current program counter
// i.e. the RIP CPU register.
func (r *Regs) PC() uint64 {
	return r.pc
}

// SP returns the stack pointer location,
// i.e. the RSP register.
func (r *Regs) SP() uint64 {
	return r.sp
}

func (r *Regs) BP() uint64 {
	return r.fp
}

// TLS returns the value of the register
// that contains the location of the thread
// local storage segment.
func (r *Regs) TLS() uint64 {
	return 0
}

func (r *Regs) GAddr() (uint64, bool) {
	return r.x28, true
}

// SetPC sets the RIP register to the value specified by `pc`.
func (thread *nativeThread) SetPC(pc uint64) error {
	kret := C.set_pc(thread.os.threadAct, C.uint64_t(pc))
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not set pc")
	}
	return nil
}

// SetSP sets the RSP register to the value specified by `pc`.
func (thread *nativeThread) SetSP(sp uint64) error {
	return errors.New("not implemented")
}

func (thread *nativeThread) SetDX(dx uint64) error {
	return errors.New("not implemented")
}

func (r *Regs) Get(n int) (uint64, error) {
	reg := arm64asm.Reg(n)
	const (
		mask8  = 0x000f
		mask16 = 0x00ff
		mask32 = 0xffff
	)

	switch reg {
	// 64-bit
	case arm64asm.X0:
		return r.x0, nil
	case arm64asm.X1:
		return r.x1, nil
	case arm64asm.X2:
		return r.x2, nil
	case arm64asm.X3:
		return r.x3, nil
	case arm64asm.X4:
		return r.x4, nil
	case arm64asm.X5:
		return r.x5, nil
	case arm64asm.X6:
		return r.x6, nil
	case arm64asm.X7:
		return r.x7, nil
	case arm64asm.X8:
		return r.x8, nil
	case arm64asm.X9:
		return r.x9, nil
	case arm64asm.X10:
		return r.x10, nil
	case arm64asm.X11:
		return r.x11, nil
	case arm64asm.X12:
		return r.x12, nil
	case arm64asm.X13:
		return r.x13, nil
	case arm64asm.X14:
		return r.x14, nil
	case arm64asm.X15:
		return r.x15, nil
	case arm64asm.X16:
		return r.x16, nil
	case arm64asm.X17:
		return r.x17, nil
	case arm64asm.X18:
		return r.x18, nil
	case arm64asm.X19:
		return r.x19, nil
	case arm64asm.X20:
		return r.x20, nil
	case arm64asm.X21:
		return r.x21, nil
	case arm64asm.X22:
		return r.x22, nil
	case arm64asm.X23:
		return r.x23, nil
	case arm64asm.X24:
		return r.x25, nil
	case arm64asm.X25:
		return r.x25, nil
	case arm64asm.X26:
		return r.x26, nil
	case arm64asm.X27:
		return r.x27, nil
	case arm64asm.X28:
		return r.x28, nil
	case arm64asm.X29:
		return r.fp, nil
	case arm64asm.X30:
		return r.lr, nil
	case arm64asm.SP:
		return r.sp, nil
	}

	return 0, proc.ErrUnknownRegister
}

func registers(thread *nativeThread) (proc.Registers, error) {
	var state C.arm_thread_state64_t
	var identity C.thread_identifier_info_data_t
	kret := C.get_registers(C.mach_port_name_t(thread.os.threadAct), &state)
	if kret != C.KERN_SUCCESS {
		return nil, fmt.Errorf("could not get registers")
	}
	kret = C.get_identity(C.mach_port_name_t(thread.os.threadAct), &identity)
	if kret != C.KERN_SUCCESS {
		return nil, fmt.Errorf("could not get thread identity informations")
	}

	regs := &Regs{
		x0:   uint64(state.__x[0]),
		x1:   uint64(state.__x[1]),
		x2:   uint64(state.__x[2]),
		x3:   uint64(state.__x[3]),
		x4:   uint64(state.__x[4]),
		x5:   uint64(state.__x[5]),
		x6:   uint64(state.__x[6]),
		x7:   uint64(state.__x[7]),
		x8:   uint64(state.__x[8]),
		x9:   uint64(state.__x[9]),
		x10:  uint64(state.__x[10]),
		x11:  uint64(state.__x[11]),
		x12:  uint64(state.__x[12]),
		x13:  uint64(state.__x[13]),
		x14:  uint64(state.__x[14]),
		x15:  uint64(state.__x[15]),
		x16:  uint64(state.__x[16]),
		x17:  uint64(state.__x[17]),
		x18:  uint64(state.__x[18]),
		x19:  uint64(state.__x[19]),
		x20:  uint64(state.__x[20]),
		x21:  uint64(state.__x[21]),
		x22:  uint64(state.__x[22]),
		x23:  uint64(state.__x[23]),
		x24:  uint64(state.__x[24]),
		x25:  uint64(state.__x[25]),
		x26:  uint64(state.__x[26]),
		x27:  uint64(state.__x[27]),
		x28:  uint64(state.__x[28]),
		fp:   uint64(state.__fp),
		lr:   uint64(state.__lr),
		sp:   uint64(state.__sp),
		pc:   uint64(state.__pc),
		cpsr: uint32(state.__cpsr),
	}

	// TODO: floating point registers

	return regs, nil
}

func (r *Regs) Copy() (proc.Registers, error) {
	//TODO(aarzilli): implement this to support function calls
	return nil, nil
}
