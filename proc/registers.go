package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
)

// Registers is an interface for a generic register type. The
// interface encapsulates the generic values / actions
// we need independent of arch. The concrete register types
// will be different depending on OS/Arch.
type Registers interface {
	PC() uint64
	SP() uint64
	CX() uint64
	TLS() uint64
	Get(int) (uint64, error)
	SetPC(*Thread, uint64) error
	Slice() []Register
}

type Register struct {
	Name  string
	Value string
}

func appendWordReg(regs []Register, name string, value uint16) []Register {
	return append(regs, Register{name, fmt.Sprintf("%#04x", value)})
}

func appendDwordReg(regs []Register, name string, value uint32) []Register {
	return append(regs, Register{name, fmt.Sprintf("%#08x", value)})
}

func appendQwordReg(regs []Register, name string, value uint64) []Register {
	return append(regs, Register{name, fmt.Sprintf("%#016x", value)})
}

func appendFlagReg(regs []Register, name string, value uint64, descr flagRegisterDescr, size int) []Register {
	return append(regs, Register{name, descr.Describe(value, size)})
}

func appendX87Reg(regs []Register, index int, exponent uint16, mantissa uint64) []Register {
	var f float64
	fset := false

	const (
		_SIGNBIT    = 1 << 15
		_EXP_BIAS   = (1 << 14) - 1 // 2^(n-1) - 1 = 16383
		_SPECIALEXP = (1 << 15) - 1 // all bits set
		_HIGHBIT    = 1 << 63
		_QUIETBIT   = 1 << 62
	)

	sign := 1.0
	if exponent&_SIGNBIT != 0 {
		sign = -1.0
	}
	exponent &= ^uint16(_SIGNBIT)

	NaN := math.NaN()
	Inf := math.Inf(+1)

	switch exponent {
	case 0:
		switch {
		case mantissa == 0:
			f = sign * 0.0
			fset = true
		case mantissa&_HIGHBIT != 0:
			f = NaN
			fset = true
		}
	case _SPECIALEXP:
		switch {
		case mantissa&_HIGHBIT == 0:
			f = sign * Inf
			fset = true
		default:
			f = NaN // signaling NaN
			fset = true
		}
	default:
		if mantissa&_HIGHBIT == 0 {
			f = NaN
			fset = true
		}
	}

	if !fset {
		significand := float64(mantissa) / (1 << 63)
		f = sign * math.Ldexp(significand, int(exponent-_EXP_BIAS))
	}

	return append(regs, Register{fmt.Sprintf("ST(%d)", index), fmt.Sprintf("%#04x%016x\t%g", exponent, mantissa, f)})
}

func appendSSEReg(regs []Register, name string, xmm []byte) []Register {
	buf := bytes.NewReader(xmm)

	var out bytes.Buffer
	var vi [16]uint8
	for i := range vi {
		binary.Read(buf, binary.LittleEndian, &vi[i])
	}

	fmt.Fprintf(&out, "0x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x", vi[15], vi[14], vi[13], vi[12], vi[11], vi[10], vi[9], vi[8], vi[7], vi[6], vi[5], vi[4], vi[3], vi[2], vi[1], vi[0])

	fmt.Fprintf(&out, "\tv2_int={ %02x%02x%02x%02x%02x%02x%02x%02x %02x%02x%02x%02x%02x%02x%02x%02x }", vi[7], vi[6], vi[5], vi[4], vi[3], vi[2], vi[1], vi[0], vi[15], vi[14], vi[13], vi[12], vi[11], vi[10], vi[9], vi[8])

	fmt.Fprintf(&out, "\tv4_int={ %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x }", vi[3], vi[2], vi[1], vi[0], vi[7], vi[6], vi[5], vi[4], vi[11], vi[10], vi[9], vi[8], vi[15], vi[14], vi[13], vi[12])

	fmt.Fprintf(&out, "\tv8_int={ %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x }", vi[1], vi[0], vi[3], vi[2], vi[5], vi[4], vi[7], vi[6], vi[9], vi[8], vi[11], vi[10], vi[13], vi[12], vi[15], vi[14])

	fmt.Fprintf(&out, "\tv16_int={ %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x }", vi[0], vi[1], vi[2], vi[3], vi[4], vi[5], vi[6], vi[7], vi[8], vi[9], vi[10], vi[11], vi[12], vi[13], vi[14], vi[15])

	buf.Seek(0, os.SEEK_SET)
	var v2 [2]float64
	for i := range v2 {
		binary.Read(buf, binary.LittleEndian, &v2[i])
	}
	fmt.Fprintf(&out, "\tv2_float={ %g %g }", v2[0], v2[1])

	buf.Seek(0, os.SEEK_SET)
	var v4 [4]float32
	for i := range v4 {
		binary.Read(buf, binary.LittleEndian, &v4[i])
	}
	fmt.Fprintf(&out, "\tv4_float={ %g %g %g %g }", v4[0], v4[1], v4[2], v4[3])

	return append(regs, Register{name, out.String()})
}

var UnknownRegisterError = errors.New("unknown register")

// Registers obtains register values from the debugged process.
func (t *Thread) Registers(floatingPoint bool) (Registers, error) {
	return registers(t, floatingPoint)
}

// PC returns the current PC for this thread.
func (t *Thread) PC() (uint64, error) {
	regs, err := t.Registers(false)
	if err != nil {
		return 0, err
	}
	return regs.PC(), nil
}

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
