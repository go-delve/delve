package ebpf

import (
	"reflect"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
)

type UProbeArgMap struct {
	Offset int64        // Offset from the stackpointer.
	Size   int64        // Size in bytes.
	Kind   reflect.Kind // Kind of variable.
	Pieces []int        // Pieces of the variables as stored in registers.
	InReg  bool         // True if this param is contained in a register.
}

type RawUProbeParam struct {
	Pieces   []op.Piece
	RealType godwarf.Type
	Kind     reflect.Kind
	Len      int64
	Base     uint64
	Addr     uint64
	Data     []byte
}

type RawUProbeParams struct {
	FnAddr      int
	InputParams []*RawUProbeParam
}
