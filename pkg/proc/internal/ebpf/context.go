package ebpf

import (
	"reflect"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
)

type UProbeArgMap struct {
	Offset    int64        // Offset from the stackpointer.
	Size      int64        // Size in bytes.
	Kind      reflect.Kind // Kind of variable.
	Pieces    []int        // Pieces of the variables as stored in registers.
	InReg     bool         // True if this param is contained in a register.
	Ret       bool         // True if this param is a return value.
	DwarfType godwarf.Type // Full DWARF type for Go-side decoding.
	Name      string       // Parameter name from DWARF.
}

type RawUProbeParam struct {
	Pieces     []op.Piece
	RealType   godwarf.Type
	DwarfType  godwarf.Type // Full DWARF type from uprobe setup (for pointer rewriting).
	Kind       reflect.Kind
	Len        int64
	Base       uint64
	Addr       uint64
	Name       string // Parameter name from DWARF.
	Data       []byte // Raw parameter value bytes.
	Unreadable error  // If set, the parameter could not be read.
}

type RawUProbeParams struct {
	FnAddr       int
	GoroutineID  int
	IsRet        bool
	InputParams  []*RawUProbeParam
	ReturnParams []*RawUProbeParam
}
