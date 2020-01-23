package proc

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/op"
)

// Registers is an interface for a generic register type. The
// interface encapsulates the generic values / actions
// we need independent of arch. The concrete register types
// will be different depending on OS/Arch.
type Registers interface {
	PC() uint64
	SP() uint64
	BP() uint64
	TLS() uint64
	// GAddr returns the address of the G variable if it is known, 0 and false otherwise
	GAddr() (uint64, bool)
	Get(int) (uint64, error)
	Slice(floatingPoint bool) []Register
	// Copy returns a copy of the registers that is guaranteed not to change
	// when the registers of the associated thread change.
	Copy() Registers
}

// Register represents a CPU register.
type Register struct {
	Name string
	Reg  *op.DwarfRegister
}

func AppendUint64Register(regs []Register, name string, value uint64) []Register {
	return append(regs, Register{name, op.DwarfRegisterFromUint64(value)})
}

func AppendBytesRegister(regs []Register, name string, value []byte) []Register {
	return append(regs, Register{name, op.DwarfRegisterFromBytes(value)})
}

// ErrUnknownRegister is returned when the value of an unknown
// register is requested.
var ErrUnknownRegister = errors.New("unknown register")

type flagRegisterDescr []flagDescr
type flagDescr struct {
	name string
	mask uint64
}

var mxcsrDescription flagRegisterDescr = []flagDescr{
	{"FZ", 1 << 15},
	{"RZ/RN", 1<<14 | 1<<13},
	{"PM", 1 << 12},
	{"UM", 1 << 11},
	{"OM", 1 << 10},
	{"ZM", 1 << 9},
	{"DM", 1 << 8},
	{"IM", 1 << 7},
	{"DAZ", 1 << 6},
	{"PE", 1 << 5},
	{"UE", 1 << 4},
	{"OE", 1 << 3},
	{"ZE", 1 << 2},
	{"DE", 1 << 1},
	{"IE", 1 << 0},
}

var eflagsDescription flagRegisterDescr = []flagDescr{
	{"CF", 1 << 0},
	{"", 1 << 1},
	{"PF", 1 << 2},
	{"AF", 1 << 4},
	{"ZF", 1 << 6},
	{"SF", 1 << 7},
	{"TF", 1 << 8},
	{"IF", 1 << 9},
	{"DF", 1 << 10},
	{"OF", 1 << 11},
	{"IOPL", 1<<12 | 1<<13},
	{"NT", 1 << 14},
	{"RF", 1 << 16},
	{"VM", 1 << 17},
	{"AC", 1 << 18},
	{"VIF", 1 << 19},
	{"VIP", 1 << 20},
	{"ID", 1 << 21},
}

func (descr flagRegisterDescr) Mask() uint64 {
	var r uint64
	for _, f := range descr {
		r = r | f.mask
	}
	return r
}

func (descr flagRegisterDescr) Describe(reg uint64, bitsize int) string {
	var r []string
	for _, f := range descr {
		if f.name == "" {
			continue
		}
		// rbm is f.mask with only the right-most bit set:
		// 0001 1100 -> 0000 0100
		rbm := f.mask & -f.mask
		if rbm == f.mask {
			if reg&f.mask != 0 {
				r = append(r, f.name)
			}
		} else {
			x := (reg & f.mask) >> uint64(math.Log2(float64(rbm)))
			r = append(r, fmt.Sprintf("%s=%x", f.name, x))
		}
	}
	if reg & ^descr.Mask() != 0 {
		r = append(r, fmt.Sprintf("unknown_flags=%x", reg&^descr.Mask()))
	}
	return fmt.Sprintf("%#0*x\t[%s]", bitsize/4, reg, strings.Join(r, " "))
}
