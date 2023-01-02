package dwarfbuilder

import (
	"bytes"

	"github.com/go-delve/delve/pkg/dwarf/leb128"
	"github.com/go-delve/delve/pkg/dwarf/op"
)

// LocEntry represents one entry of debug_loc.
type LocEntry struct {
	Lowpc  uint64
	Highpc uint64
	Loc    []byte
}

// LocationBlock returns a DWARF expression corresponding to the list of
// arguments.
func LocationBlock(args ...interface{}) []byte {
	var buf bytes.Buffer
	for _, arg := range args {
		switch x := arg.(type) {
		case op.Opcode:
			buf.WriteByte(byte(x))
		case int:
			leb128.EncodeSigned(&buf, int64(x))
		case uint:
			leb128.EncodeUnsigned(&buf, uint64(x))
		default:
			panic("unsupported value type")
		}
	}
	return buf.Bytes()
}
