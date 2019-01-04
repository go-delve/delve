package dwarfbuilder

import (
	"bytes"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/util"
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
			util.EncodeSLEB128(&buf, int64(x))
		case uint:
			util.EncodeULEB128(&buf, uint64(x))
		default:
			panic("unsupported value type")
		}
	}
	return buf.Bytes()
}
