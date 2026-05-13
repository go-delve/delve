package ebpf

import (
	"reflect"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
)

// DerefEntry describes a single pointer dereference within a parameter value.
// Offset is the byte position within the parameter's raw value where the
// pointer lives. Size is how many bytes to read from the pointed-to address.
type DerefEntry struct {
	Offset uint32
	Size   uint32
}

type UProbeArgMap struct {
	Offset    int64         // Offset from the stackpointer.
	Size      int64         // Size in bytes.
	Kind      reflect.Kind  // Kind of variable.
	Pieces    []int         // Pieces of the variables as stored in registers.
	InReg     bool          // True if this param is contained in a register.
	Ret       bool          // True if this param is a return value.
	Derefs    [8]DerefEntry // Dereference plan: pointer fields to chase.
	NumDerefs uint32        // Number of valid entries in Derefs.
	DwarfType godwarf.Type  // Full DWARF type for Go-side decoding.
}

type RawUProbeParam struct {
	Pieces     []op.Piece
	RealType   godwarf.Type
	DwarfType  godwarf.Type // Full DWARF type from uprobe setup (for pointer rewriting).
	Kind       reflect.Kind
	Len        int64
	Base       uint64
	Addr       uint64
	Data       []byte // Raw parameter value bytes (variable length).
	DerefData  []byte // Concatenated dereference results (variable length).
	Unreadable error  // If set, the parameter could not be read.
}

type RawUProbeParams struct {
	FnAddr       int
	GoroutineID  int
	IsRet        bool
	InputParams  []*RawUProbeParam
	ReturnParams []*RawUProbeParam
}
