package debugger

import (
	"fmt"
	"go/constant"
	"math/big"
	"time"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/service/api"
)

func FormatAndConvertVar(v *proc.Variable, arch proc.Arch) *api.Variable {
	r, hasChildren := api.ConvertOneVar(v)

	switch r.RealType {
	case "time.Time":
		r.Value = formatTime(v)
	case "math/big.Int":
		r.Value = formatBigInt(v, arch)
	case "math/big.Float":
		r.Value = formatBigFloat(v, arch)
	case "math/big.Rat":
		r.Value = formatBigRat(v, arch)
	}

	if hasChildren {
		r.Children = make([]api.Variable, len(v.Children))

		for i := range v.Children {
			r.Children[i] = *FormatAndConvertVar(&v.Children[i], arch)
		}
	}
	return r
}

const (
	timeTimeWallHasMonotonicBit uint64 = (1 << 63) // hasMonotonic bit of time.Time.wall

	maxAddSeconds time.Duration = (time.Duration(^uint64(0)>>1) / time.Second) * time.Second // maximum number of seconds that can be added with (time.Time).Add, measured in nanoseconds

	wallNsecShift = 30 // size of the nanoseconds field of time.Time.wall

	unixTimestampOfWallEpoch = -2682288000 // number of seconds between the unix epoch and the epoch for time.Time.wall (1 jan 1885)
)

func fieldVariable(v *proc.Variable, name string) *proc.Variable {
	for i := range v.Children {
		if child := &v.Children[i]; child.Name == name {
			return child
		}
	}
	return nil
}

// formatTime returns formatted value of a time.Time variable.
// See $GOROOT/src/time/time.go for a description of time.Time internals.
func formatTime(v *proc.Variable) string {
	wallv := fieldVariable(v, "wall")
	extv := fieldVariable(v, "ext")
	if wallv == nil || extv == nil || wallv.Unreadable != nil || extv.Unreadable != nil || wallv.Value == nil || extv.Value == nil {
		return ""
	}

	wall, _ := constant.Uint64Val(wallv.Value)
	ext, _ := constant.Int64Val(extv.Value)
	_ = ext

	hasMonotonic := (wall & timeTimeWallHasMonotonicBit) != 0
	if hasMonotonic {
		// the 33-bit field of wall holds a 33-bit unsigned wall
		// seconds since Jan 1 year 1885, and ext holds a signed 64-bit monotonic
		// clock reading, nanoseconds since process start
		sec := int64(wall << 1 >> (wallNsecShift + 1)) // seconds since 1 Jan 1885
		t := time.Unix(sec+unixTimestampOfWallEpoch, 0).UTC()
		return fmt.Sprintf("time.Time(%s, %+d)", t.Format(time.RFC3339), ext)
	} else {
		// the full signed 64-bit wall seconds since Jan 1 year 1 is stored in ext
		var t time.Time
		for ext > int64(maxAddSeconds/time.Second) {
			t = t.Add(maxAddSeconds)
			ext -= int64(maxAddSeconds / time.Second)
		}
		t = t.Add(time.Duration(ext) * time.Second)
		return t.Format(time.RFC3339)
	}
}

var len8tab = [256]uint8{
	0x00, 0x01, 0x02, 0x02, 0x03, 0x03, 0x03, 0x03, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04,
	0x05, 0x05, 0x05, 0x05, 0x05, 0x05, 0x05, 0x05, 0x05, 0x05, 0x05, 0x05, 0x05, 0x05, 0x05, 0x05,
	0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06,
	0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06, 0x06,
	0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07,
	0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07,
	0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07,
	0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07,
	0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08,
	0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08,
	0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08,
	0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08,
	0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08,
	0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08,
	0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08,
	0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08,
}

// len64 is a copy of math/bits.Len64 copied here for backwards
// compatibility.
func len64(x uint64) (n int) {
	if x >= 1<<32 {
		x >>= 32
		n = 32
	}
	if x >= 1<<16 {
		x >>= 16
		n += 16
	}
	if x >= 1<<8 {
		x >>= 8
		n += 8
	}
	return n + int(len8tab[x])
}

// readNat reads a math/big.nat slice.
func readNat(absv *proc.Variable, arch proc.Arch) (n *big.Int, bitsize int64) {
	// _B here tracks _B in $GOROOT/src/math/big/arith.go
	_B := big.NewInt(1)
	_B.Lsh(_B, uint(arch.PtrSize()*8))

	ptrbits := arch.PtrSize() * 8
	bitsize = int64((len(absv.Children) - 1) * ptrbits)

	n = big.NewInt(0)
	x := big.NewInt(0)
	for i := len(absv.Children) - 1; i >= 0; i-- {
		n.Mul(n, _B)
		ix, _ := constant.Uint64Val(absv.Children[i].Value)
		if i == len(absv.Children)-1 {
			bitsize += int64(len64(ix))
		}
		x.SetUint64(ix)
		n.Add(n, x)
	}
	return n, bitsize
}

func asBigInt(v *proc.Variable, arch proc.Arch) *big.Int {
	negv := fieldVariable(v, "neg")
	absv := fieldVariable(v, "abs")

	if negv == nil || negv.Value == nil || negv.Unreadable != nil || absv == nil || absv.Unreadable != nil || absv.Len != int64(len(absv.Children)) {
		return nil
	}

	n, _ := readNat(absv, arch)
	if n == nil {
		return nil
	}
	if constant.BoolVal(negv.Value) {
		n.Mul(n, big.NewInt(-1))
	}
	return n
}

// formatBigInt returns the formatted value of a math/big.Int variable.
// See $GOROOT/src/math/big/int.go and $GOROOT/stc/math/big/nat.go for a
// description of math/big.Int internals.
func formatBigInt(v *proc.Variable, arch proc.Arch) string {
	n := asBigInt(v, arch)
	if n == nil {
		return ""
	}
	return n.String()
}

// formatBigRat returne sthe formatted value of a math/big.Rat variable.
// See $GOROOT/src/math/big/rat.go for a description of math/big.Rat internals.
func formatBigRat(v *proc.Variable, arch proc.Arch) string {
	nomv := fieldVariable(v, "a")
	denomv := fieldVariable(v, "b")

	if nomv == nil || nomv.Unreadable != nil || int64(len(nomv.Children)) != nomv.Len || denomv == nil || denomv.Unreadable != nil || int64(len(denomv.Children)) != denomv.Len {
		return ""
	}

	nom := asBigInt(nomv, arch)
	denom := asBigInt(denomv, arch)

	if nom == nil || denom == nil {
		return ""
	}

	return nom.String() + "/" + denom.String()
}

// formatBigFloat returns the formatted value of a math/big.Float variable.
// See $GOROOT/src/math/big/float.go for a description of math/big.Float internals.
func formatBigFloat(v *proc.Variable, arch proc.Arch) string {
	//TODO: implement
	negv := fieldVariable(v, "neg")
	mantv := fieldVariable(v, "mant")
	expv := fieldVariable(v, "exp")
	formv := fieldVariable(v, "form")
	precv := fieldVariable(v, "prec")

	for _, vv := range []*proc.Variable{negv, expv, formv, precv} {
		if vv == nil || vv.Unreadable != nil || vv.Value == nil {
			return ""
		}
	}
	if mantv == nil || mantv.Unreadable != nil || mantv.Len != int64(len(mantv.Children)) {
		return ""
	}

	form, _ := constant.Int64Val(formv.Value)
	exp, _ := constant.Int64Val(expv.Value)
	prec, _ := constant.Int64Val(precv.Value)

	// see type math/big.form in $GOROOT/src/math/big/float.go
	switch form {
	case 0: // zero
		return "0"
	case 2: // infinite
		if constant.BoolVal(negv.Value) {
			return "-inf"
		}
		return "+inf"
	}

	mant, mantbits := readNat(mantv, arch)
	if mant == nil {
		return ""
	}

	if constant.BoolVal(negv.Value) {
		mant.Mul(mant, big.NewInt(-1))
	}

	x := new(big.Float).SetMantExp(new(big.Float).SetInt(mant), int(exp-mantbits))
	x.SetPrec(uint(prec))
	return x.String()
}
