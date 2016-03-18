package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"go/constant"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"unsafe"

	"golang.org/x/debug/dwarf"

	"github.com/derekparker/delve/dwarf/op"
	"github.com/derekparker/delve/dwarf/reader"
)

const (
	maxVariableRecurse = 1  // How far to recurse when evaluating nested types.
	maxArrayValues     = 64 // Max value for reading large arrays.
	maxErrCount        = 3  // Max number of read errors to accept while evaluating slices, arrays and structs

	maxArrayStridePrefetch = 1024 // Maximum size of array stride for which we will prefetch the array contents

	chanRecv = "chan receive"
	chanSend = "chan send"

	hashTophashEmpty = 0 // used by map reading code, indicates an empty bucket
	hashMinTopHash   = 4 // used by map reading code, indicates minimum value of tophash that isn't empty or evacuated
)

// Variable represents a variable. It contains the address, name,
// type and other information parsed from both the Dwarf information
// and the memory of the debugged process.
// If OnlyAddr is true, the variables value has not been loaded.
type Variable struct {
	Addr      uintptr
	OnlyAddr  bool
	Name      string
	DwarfType dwarf.Type
	RealType  dwarf.Type
	Kind      reflect.Kind
	mem       memoryReadWriter
	dbp       *Process

	Value constant.Value

	Len int64
	Cap int64

	// Base address of arrays, Base address of the backing array for slices (0 for nil slices)
	// Base address of the backing byte array for strings
	// address of the struct backing chan and map variables
	// address of the function entry point for function variables (0 for nil function pointers)
	Base      uintptr
	stride    int64
	fieldType dwarf.Type

	// number of elements to skip when loading a map
	mapSkip int

	Children []Variable

	loaded     bool
	Unreadable error
}

// M represents a runtime M (OS thread) structure.
type M struct {
	procid   int     // Thread ID or port.
	spinning uint8   // Busy looping.
	blocked  uint8   // Waiting on futex / semaphore.
	curg     uintptr // Current G running on this thread.
}

// G status, from: src/runtime/runtime2.go
const (
	Gidle           uint64 = iota // 0
	Grunnable                     // 1 runnable and on a run queue
	Grunning                      // 2
	Gsyscall                      // 3
	Gwaiting                      // 4
	GmoribundUnused               // 5 currently unused, but hardcoded in gdb scripts
	Gdead                         // 6
	Genqueue                      // 7 Only the Gscanenqueue is used.
	Gcopystack                    // 8 in this state when newstack is moving the stack
)

// G represents a runtime G (goroutine) structure (at least the
// fields that Delve is interested in).
type G struct {
	ID         int    // Goroutine ID
	PC         uint64 // PC of goroutine when it was parked.
	SP         uint64 // SP of goroutine when it was parked.
	GoPC       uint64 // PC of 'go' statement that created this goroutine.
	WaitReason string // Reason for goroutine being parked.
	Status     uint64

	// Information on goroutine location
	CurrentLoc Location

	// PC of entry to top-most deferred function.
	DeferPC uint64

	// Thread that this goroutine is currently allocated to
	thread *Thread

	dbp *Process
}

// EvalScope is the scope for variable evaluation. Contains the thread,
// current location (PC), and canonical frame address.
type EvalScope struct {
	Thread *Thread
	PC     uint64
	CFA    int64
}

// IsNilErr is returned when a variable is nil.
type IsNilErr struct {
	name string
}

func (err *IsNilErr) Error() string {
	return fmt.Sprintf("%s is nil", err.name)
}

func (scope *EvalScope) newVariable(name string, addr uintptr, dwarfType dwarf.Type) *Variable {
	return newVariable(name, addr, dwarfType, scope.Thread.dbp, scope.Thread)
}

func (t *Thread) newVariable(name string, addr uintptr, dwarfType dwarf.Type) *Variable {
	return newVariable(name, addr, dwarfType, t.dbp, t)
}

func (v *Variable) newVariable(name string, addr uintptr, dwarfType dwarf.Type) *Variable {
	return newVariable(name, addr, dwarfType, v.dbp, v.mem)
}

func newVariable(name string, addr uintptr, dwarfType dwarf.Type, dbp *Process, mem memoryReadWriter) *Variable {
	v := &Variable{
		Name:      name,
		Addr:      addr,
		DwarfType: dwarfType,
		mem:       mem,
		dbp:       dbp,
	}

	v.RealType = resolveTypedef(v.DwarfType)

	switch t := v.RealType.(type) {
	case *dwarf.PtrType:
		v.Kind = reflect.Ptr
		if _, isvoid := t.Type.(*dwarf.VoidType); isvoid {
			v.Kind = reflect.UnsafePointer
		}
	case *dwarf.ChanType:
		v.Kind = reflect.Chan
	case *dwarf.MapType:
		v.Kind = reflect.Map
	case *dwarf.StringType:
		v.Kind = reflect.String
		v.stride = 1
		v.fieldType = &dwarf.UintType{BasicType: dwarf.BasicType{CommonType: dwarf.CommonType{ByteSize: 1, Name: "byte"}, BitSize: 8, BitOffset: 0}}
		if v.Addr != 0 {
			v.Base, v.Len, v.Unreadable = readStringInfo(v.mem, v.dbp.arch, v.Addr)
		}
	case *dwarf.SliceType:
		v.Kind = reflect.Slice
		if v.Addr != 0 {
			v.loadSliceInfo(t)
		}
	case *dwarf.InterfaceType:
		v.Kind = reflect.Interface
	case *dwarf.StructType:
		v.Kind = reflect.Struct
	case *dwarf.ArrayType:
		v.Kind = reflect.Array
		v.Base = v.Addr
		v.Len = t.Count
		v.Cap = -1
		v.fieldType = t.Type
		v.stride = 0

		if t.Count > 0 {
			v.stride = t.ByteSize / t.Count
		}
	case *dwarf.ComplexType:
		switch t.ByteSize {
		case 8:
			v.Kind = reflect.Complex64
		case 16:
			v.Kind = reflect.Complex128
		}
	case *dwarf.IntType:
		v.Kind = reflect.Int
	case *dwarf.UintType:
		v.Kind = reflect.Uint
	case *dwarf.FloatType:
		switch t.ByteSize {
		case 4:
			v.Kind = reflect.Float32
		case 8:
			v.Kind = reflect.Float64
		}
	case *dwarf.BoolType:
		v.Kind = reflect.Bool
	case *dwarf.FuncType:
		v.Kind = reflect.Func
	case *dwarf.VoidType:
		v.Kind = reflect.Invalid
	case *dwarf.UnspecifiedType:
		v.Kind = reflect.Invalid
	default:
		v.Unreadable = fmt.Errorf("Unknown type: %T", t)
	}

	return v
}

func resolveTypedef(typ dwarf.Type) dwarf.Type {
	for {
		if tt, ok := typ.(*dwarf.TypedefType); ok {
			typ = tt.Type
		} else {
			return typ
		}
	}
}

func newConstant(val constant.Value, mem memoryReadWriter) *Variable {
	v := &Variable{Value: val, mem: mem, loaded: true}
	switch val.Kind() {
	case constant.Int:
		v.Kind = reflect.Int
	case constant.Float:
		v.Kind = reflect.Float64
	case constant.Bool:
		v.Kind = reflect.Bool
	case constant.Complex:
		v.Kind = reflect.Complex128
	case constant.String:
		v.Kind = reflect.String
		v.Len = int64(len(constant.StringVal(val)))
	}
	return v
}

var nilVariable = &Variable{
	Addr:     0,
	Base:     0,
	Kind:     reflect.Ptr,
	Children: []Variable{{Addr: 0, OnlyAddr: true}},
}

func (v *Variable) clone() *Variable {
	r := *v
	return &r
}

// TypeString returns the string representation
// of the type of this variable.
func (v *Variable) TypeString() string {
	if v == nilVariable {
		return "nil"
	}
	if v.DwarfType != nil {
		return v.DwarfType.String()
	}
	return v.Kind.String()
}

func (v *Variable) toField(field *dwarf.StructField) (*Variable, error) {
	if v.Unreadable != nil {
		return v.clone(), nil
	}
	if v.Addr == 0 {
		return nil, &IsNilErr{v.Name}
	}

	name := ""
	if v.Name != "" {
		parts := strings.Split(field.Name, ".")
		if len(parts) > 1 {
			name = fmt.Sprintf("%s.%s", v.Name, parts[1])
		} else {
			name = fmt.Sprintf("%s.%s", v.Name, field.Name)
		}
	}
	return v.newVariable(name, uintptr(int64(v.Addr)+field.ByteOffset), field.Type), nil
}

// DwarfReader returns the DwarfReader containing the
// Dwarf information for the target process.
func (scope *EvalScope) DwarfReader() *reader.Reader {
	return scope.Thread.dbp.DwarfReader()
}

// Type returns the Dwarf type entry at `offset`.
func (scope *EvalScope) Type(offset dwarf.Offset) (dwarf.Type, error) {
	return scope.Thread.dbp.dwarf.Type(offset)
}

// PtrSize returns the size of a pointer.
func (scope *EvalScope) PtrSize() int {
	return scope.Thread.dbp.arch.PtrSize()
}

// ChanRecvBlocked returns whether the goroutine is blocked on
// a channel read operation.
func (g *G) ChanRecvBlocked() bool {
	return (g.thread == nil) && (g.WaitReason == chanRecv)
}

// chanRecvReturnAddr returns the address of the return from a channel read.
func (g *G) chanRecvReturnAddr(dbp *Process) (uint64, error) {
	locs, err := dbp.GoroutineStacktrace(g, 4)
	if err != nil {
		return 0, err
	}
	topLoc := locs[len(locs)-1]
	return topLoc.Current.PC, nil
}

// NoGError returned when a G could not be found
// for a specific thread.
type NoGError struct {
	tid int
}

func (ng NoGError) Error() string {
	return fmt.Sprintf("no G executing on thread %d", ng.tid)
}

func (gvar *Variable) parseG() (*G, error) {
	mem := gvar.mem
	dbp := gvar.dbp
	gaddr := uint64(gvar.Addr)
	_, deref := gvar.RealType.(*dwarf.PtrType)

	if deref {
		gaddrbytes, err := mem.readMemory(uintptr(gaddr), dbp.arch.PtrSize())
		if err != nil {
			return nil, fmt.Errorf("error derefing *G %s", err)
		}
		gaddr = binary.LittleEndian.Uint64(gaddrbytes)
	}
	if gaddr == 0 {
		id := 0
		if thread, ok := mem.(*Thread); ok {
			id = thread.ID
		}
		return nil, NoGError{ tid: id }
	}
	gvar.loadValue()
	if gvar.Unreadable != nil {
		return nil, gvar.Unreadable
	}
	schedVar := gvar.toFieldNamed("sched")
	pc, _ := constant.Int64Val(schedVar.toFieldNamed("pc").Value)
	sp, _ := constant.Int64Val(schedVar.toFieldNamed("sp").Value)
	id, _ := constant.Int64Val(gvar.toFieldNamed("goid").Value)
	gopc, _ := constant.Int64Val(gvar.toFieldNamed("gopc").Value)
	waitReason := constant.StringVal(gvar.toFieldNamed("waitreason").Value)
	d := gvar.toFieldNamed("_defer")
	deferPC := int64(0)
	fnvar := d.toFieldNamed("fn")
	if fnvar != nil {
		fnvalvar := fnvar.toFieldNamed("fn")
		deferPC, _ = constant.Int64Val(fnvalvar.Value)
	}
	status, _ := constant.Int64Val(gvar.toFieldNamed("atomicstatus").Value)
	f, l, fn := gvar.dbp.goSymTable.PCToLine(uint64(pc))
	g := &G{
		ID:         int(id),
		GoPC:       uint64(gopc),
		PC:         uint64(pc),
		SP:         uint64(sp),
		WaitReason: waitReason,
		DeferPC:    uint64(deferPC),
		Status:     uint64(status),
		CurrentLoc: Location{PC: uint64(pc), File: f, Line: l, Fn: fn},
		dbp:        gvar.dbp,
	}
	return g, nil
}

func (v *Variable) toFieldNamed(name string) *Variable {
	v, err := v.structMember(name)
	if err != nil {
		return nil
	}
	v.loadValue()
	if v.Unreadable != nil {
		return nil
	}
	return v
}

// From $GOROOT/src/runtime/traceback.go:597
// isExportedRuntime reports whether name is an exported runtime function.
// It is only for runtime functions, so ASCII A-Z is fine.
func isExportedRuntime(name string) bool {
	const n = len("runtime.")
	return len(name) > n && name[:n] == "runtime." && 'A' <= name[n] && name[n] <= 'Z'
}

// UserCurrent returns the location the users code is at,
// or was at before entering a runtime function.
func (g *G) UserCurrent() Location {
	pc, sp := g.PC, g.SP
	if g.thread != nil {
		regs, err := g.thread.Registers()
		if err != nil {
			return g.CurrentLoc
		}
		pc, sp = regs.PC(), regs.SP()
	}
	it := newStackIterator(g.dbp, pc, sp)
	for it.Next() {
		frame := it.Frame()
		if frame.Call.Fn != nil {
			name := frame.Call.Fn.Name
			if (strings.Index(name, ".") >= 0) && (!strings.HasPrefix(name, "runtime.") || isExportedRuntime(name)) {
				return frame.Call
			}
		}
	}
	return g.CurrentLoc
}

// Go returns the location of the 'go' statement
// that spawned this goroutine.
func (g *G) Go() Location {
	f, l, fn := g.dbp.goSymTable.PCToLine(g.GoPC)
	return Location{PC: g.GoPC, File: f, Line: l, Fn: fn}
}

// EvalVariable returns the value of the given expression (backwards compatibility).
func (scope *EvalScope) EvalVariable(name string) (*Variable, error) {
	return scope.EvalExpression(name)
}

// SetVariable sets the value of the named variable
func (scope *EvalScope) SetVariable(name, value string) error {
	t, err := parser.ParseExpr(name)
	if err != nil {
		return err
	}

	xv, err := scope.evalAST(t)
	if err != nil {
		return err
	}

	if xv.Addr == 0 {
		return fmt.Errorf("Can not assign to \"%s\"", name)
	}

	if xv.Unreadable != nil {
		return fmt.Errorf("Expression \"%s\" is unreadable: %v", name, xv.Unreadable)
	}

	t, err = parser.ParseExpr(value)
	if err != nil {
		return err
	}

	yv, err := scope.evalAST(t)
	if err != nil {
		return err
	}

	yv.loadValue()

	if err := yv.isType(xv.RealType, xv.Kind); err != nil {
		return err
	}

	if yv.Unreadable != nil {
		return fmt.Errorf("Expression \"%s\" is unreadable: %v", value, yv.Unreadable)
	}

	return xv.setValue(yv)
}

func (scope *EvalScope) extractVariableFromEntry(entry *dwarf.Entry) (*Variable, error) {
	rdr := scope.DwarfReader()
	v, err := scope.extractVarInfoFromEntry(entry, rdr)
	if err != nil {
		return nil, err
	}
	v.loadValue()
	return v, nil
}

func (scope *EvalScope) extractVarInfo(varName string) (*Variable, error) {
	reader := scope.DwarfReader()

	_, err := reader.SeekToFunction(scope.PC)
	if err != nil {
		return nil, err
	}

	for entry, err := reader.NextScopeVariable(); entry != nil; entry, err = reader.NextScopeVariable() {
		if err != nil {
			return nil, err
		}

		n, ok := entry.Val(dwarf.AttrName).(string)
		if !ok {
			continue
		}

		if n == varName {
			return scope.extractVarInfoFromEntry(entry, reader)
		}
	}
	return nil, fmt.Errorf("could not find symbol value for %s", varName)
}

// LocalVariables returns all local variables from the current function scope.
func (scope *EvalScope) LocalVariables() ([]*Variable, error) {
	return scope.variablesByTag(dwarf.TagVariable)
}

// FunctionArguments returns the name, value, and type of all current function arguments.
func (scope *EvalScope) FunctionArguments() ([]*Variable, error) {
	return scope.variablesByTag(dwarf.TagFormalParameter)
}

// PackageVariables returns the name, value, and type of all package variables in the application.
func (scope *EvalScope) PackageVariables() ([]*Variable, error) {
	var vars []*Variable
	reader := scope.DwarfReader()

	var utypoff dwarf.Offset
	utypentry, err := reader.SeekToTypeNamed("<unspecified>")
	if err == nil {
		utypoff = utypentry.Offset
	}

	for entry, err := reader.NextPackageVariable(); entry != nil; entry, err = reader.NextPackageVariable() {
		if err != nil {
			return nil, err
		}

		if typoff, ok := entry.Val(dwarf.AttrType).(dwarf.Offset); !ok || typoff == utypoff {
			continue
		}

		// Ignore errors trying to extract values
		val, err := scope.extractVariableFromEntry(entry)
		if err != nil {
			continue
		}
		vars = append(vars, val)
	}

	return vars, nil
}

// EvalPackageVariable will evaluate the package level variable
// specified by 'name'.
func (dbp *Process) EvalPackageVariable(name string) (*Variable, error) {
	scope := &EvalScope{Thread: dbp.CurrentThread, PC: 0, CFA: 0}

	v, err := scope.packageVarAddr(name)
	if err != nil {
		return nil, err
	}
	v.loadValue()
	return v, nil
}

func (scope *EvalScope) packageVarAddr(name string) (*Variable, error) {
	reader := scope.DwarfReader()
	for entry, err := reader.NextPackageVariable(); entry != nil; entry, err = reader.NextPackageVariable() {
		if err != nil {
			return nil, err
		}

		n, ok := entry.Val(dwarf.AttrName).(string)
		if !ok {
			continue
		}

		if n == name {
			return scope.extractVarInfoFromEntry(entry, reader)
		}
	}
	return nil, fmt.Errorf("could not find symbol value for %s", name)
}

func (v *Variable) structMember(memberName string) (*Variable, error) {
	if v.Unreadable != nil {
		return v.clone(), nil
	}
	structVar := v.maybeDereference()
	structVar.Name = v.Name
	if structVar.Unreadable != nil {
		return structVar, nil
	}

	switch t := structVar.RealType.(type) {
	case *dwarf.StructType:
		for _, field := range t.Field {
			if field.Name != memberName {
				continue
			}
			return structVar.toField(field)
		}
		// Check for embedded field only if field was
		// not a regular struct member
		for _, field := range t.Field {
			isEmbeddedStructMember :=
				(field.Type.String() == ("struct " + field.Name)) ||
					(len(field.Name) > 1 &&
						field.Name[0] == '*' &&
						field.Type.String()[1:] == ("struct "+field.Name[1:]))
			if !isEmbeddedStructMember {
				continue
			}
			// Check for embedded field referenced by type name
			parts := strings.Split(field.Name, ".")
			if len(parts) > 1 && parts[1] == memberName {
				embeddedVar, err := structVar.toField(field)
				if err != nil {
					return nil, err
				}
				return embeddedVar, nil
			}
			// Recursively check for promoted fields on the embedded field
			embeddedVar, err := structVar.toField(field)
			if err != nil {
				return nil, err
			}
			embeddedVar.Name = structVar.Name
			embeddedField, err := embeddedVar.structMember(memberName)
			if embeddedField != nil {
				return embeddedField, nil
			}
		}
		return nil, fmt.Errorf("%s has no member %s", v.Name, memberName)
	default:
		if v.Name == "" {
			return nil, fmt.Errorf("type %s is not a struct", structVar.TypeString())
		}
		return nil, fmt.Errorf("%s (type %s) is not a struct", v.Name, structVar.TypeString())
	}
}

// Extracts the name and type of a variable from a dwarf entry
// then executes the instructions given in the  DW_AT_location attribute to grab the variable's address
func (scope *EvalScope) extractVarInfoFromEntry(entry *dwarf.Entry, rdr *reader.Reader) (*Variable, error) {
	if entry == nil {
		return nil, fmt.Errorf("invalid entry")
	}

	if entry.Tag != dwarf.TagFormalParameter && entry.Tag != dwarf.TagVariable {
		return nil, fmt.Errorf("invalid entry tag, only supports FormalParameter and Variable, got %s", entry.Tag.String())
	}

	n, ok := entry.Val(dwarf.AttrName).(string)
	if !ok {
		return nil, fmt.Errorf("type assertion failed")
	}

	offset, ok := entry.Val(dwarf.AttrType).(dwarf.Offset)
	if !ok {
		return nil, fmt.Errorf("type assertion failed")
	}

	t, err := scope.Type(offset)
	if err != nil {
		return nil, err
	}

	instructions, ok := entry.Val(dwarf.AttrLocation).([]byte)
	if !ok {
		return nil, fmt.Errorf("type assertion failed")
	}

	addr, err := op.ExecuteStackProgram(scope.CFA, instructions)
	if err != nil {
		return nil, err
	}

	return scope.newVariable(n, uintptr(addr), t), nil
}

// If v is a pointer a new variable is returned containing the value pointed by v.
func (v *Variable) maybeDereference() *Variable {
	if v.Unreadable != nil {
		return v
	}

	switch t := v.RealType.(type) {
	case *dwarf.PtrType:
		ptrval, err := readUintRaw(v.mem, uintptr(v.Addr), t.ByteSize)
		r := v.newVariable("", uintptr(ptrval), t.Type)
		if err != nil {
			r.Unreadable = err
		}

		return r
	default:
		return v
	}
}

// Extracts the value of the variable at the given address.
func (v *Variable) loadValue() {
	v.loadValueInternal(0)
}

func (v *Variable) loadValueInternal(recurseLevel int) {
	if v.Unreadable != nil || v.loaded || (v.Addr == 0 && v.Base == 0) {
		return
	}

	v.loaded = true
	switch v.Kind {
	case reflect.Ptr, reflect.UnsafePointer:
		v.Len = 1
		v.Children = []Variable{*v.maybeDereference()}
		// Don't increase the recursion level when dereferencing pointers
		v.Children[0].loadValueInternal(recurseLevel)

	case reflect.Chan:
		sv := v.clone()
		sv.RealType = resolveTypedef(&(sv.RealType.(*dwarf.ChanType).TypedefType))
		sv = sv.maybeDereference()
		sv.loadValueInternal(recurseLevel)
		v.Children = sv.Children
		v.Len = sv.Len
		v.Base = sv.Addr

	case reflect.Map:
		if recurseLevel <= maxVariableRecurse {
			v.loadMap(recurseLevel)
		}

	case reflect.String:
		var val string
		val, v.Unreadable = readStringValue(v.mem, v.Base, v.Len)
		v.Value = constant.MakeString(val)

	case reflect.Slice, reflect.Array:
		v.loadArrayValues(recurseLevel)

	case reflect.Struct:
		v.mem = cacheMemory(v.mem, v.Addr, int(v.RealType.Size()))
		t := v.RealType.(*dwarf.StructType)
		v.Len = int64(len(t.Field))
		// Recursively call extractValue to grab
		// the value of all the members of the struct.
		if recurseLevel <= maxVariableRecurse {
			v.Children = make([]Variable, 0, len(t.Field))
			for i, field := range t.Field {
				f, _ := v.toField(field)
				v.Children = append(v.Children, *f)
				v.Children[i].Name = field.Name
				v.Children[i].loadValueInternal(recurseLevel + 1)
			}
		}

	case reflect.Interface:
		v.loadInterface(recurseLevel, true)

	case reflect.Complex64, reflect.Complex128:
		v.readComplex(v.RealType.(*dwarf.ComplexType).ByteSize)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var val int64
		val, v.Unreadable = readIntRaw(v.mem, v.Addr, v.RealType.(*dwarf.IntType).ByteSize)
		v.Value = constant.MakeInt64(val)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var val uint64
		val, v.Unreadable = readUintRaw(v.mem, v.Addr, v.RealType.(*dwarf.UintType).ByteSize)
		v.Value = constant.MakeUint64(val)

	case reflect.Bool:
		val, err := v.mem.readMemory(v.Addr, 1)
		v.Unreadable = err
		if err == nil {
			v.Value = constant.MakeBool(val[0] != 0)
		}
	case reflect.Float32, reflect.Float64:
		var val float64
		val, v.Unreadable = v.readFloatRaw(v.RealType.(*dwarf.FloatType).ByteSize)
		v.Value = constant.MakeFloat64(val)
	case reflect.Func:
		v.readFunctionPtr()
	default:
		v.Unreadable = fmt.Errorf("unknown or unsupported kind: \"%s\"", v.Kind.String())
	}
}

func (v *Variable) setValue(y *Variable) error {
	var err error
	switch v.Kind {
	case reflect.Float32, reflect.Float64:
		f, _ := constant.Float64Val(y.Value)
		err = v.writeFloatRaw(f, v.RealType.Size())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, _ := constant.Int64Val(y.Value)
		err = v.writeUint(uint64(n), v.RealType.Size())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, _ := constant.Uint64Val(y.Value)
		err = v.writeUint(n, v.RealType.Size())
	case reflect.Bool:
		err = v.writeBool(constant.BoolVal(y.Value))
	case reflect.Complex64, reflect.Complex128:
		real, _ := constant.Float64Val(constant.Real(y.Value))
		imag, _ := constant.Float64Val(constant.Imag(y.Value))
		err = v.writeComplex(real, imag, v.RealType.Size())
	default:
		fmt.Printf("default\n")
		if t, isptr := v.RealType.(*dwarf.PtrType); isptr {
			err = v.writeUint(uint64(y.Children[0].Addr), int64(t.ByteSize))
		} else {
			return fmt.Errorf("can not set variables of type %s (not implemented)", v.Kind.String())
		}
	}

	return err
}

func readStringInfo(mem memoryReadWriter, arch Arch, addr uintptr) (uintptr, int64, error) {
	// string data structure is always two ptrs in size. Addr, followed by len
	// http://research.swtch.com/godata

	mem = cacheMemory(mem, addr, arch.PtrSize()*2)

	// read len
	val, err := mem.readMemory(addr+uintptr(arch.PtrSize()), arch.PtrSize())
	if err != nil {
		return 0, 0, fmt.Errorf("could not read string len %s", err)
	}
	strlen := int64(binary.LittleEndian.Uint64(val))
	if strlen < 0 {
		return 0, 0, fmt.Errorf("invalid length: %d", strlen)
	}

	// read addr
	val, err = mem.readMemory(addr, arch.PtrSize())
	if err != nil {
		return 0, 0, fmt.Errorf("could not read string pointer %s", err)
	}
	addr = uintptr(binary.LittleEndian.Uint64(val))
	if addr == 0 {
		return 0, 0, nil
	}

	return addr, strlen, nil
}

func readStringValue(mem memoryReadWriter, addr uintptr, strlen int64) (string, error) {
	count := strlen
	if count > maxArrayValues {
		count = maxArrayValues
	}

	val, err := mem.readMemory(addr, int(count))
	if err != nil {
		return "", fmt.Errorf("could not read string at %#v due to %s", addr, err)
	}

	retstr := *(*string)(unsafe.Pointer(&val))

	return retstr, nil
}

func readString(mem memoryReadWriter, arch Arch, addr uintptr) (string, int64, error) {
	addr, strlen, err := readStringInfo(mem, arch, addr)
	if err != nil {
		return "", 0, err
	}

	retstr, err := readStringValue(mem, addr, strlen)
	return retstr, strlen, err
}

func (v *Variable) loadSliceInfo(t *dwarf.SliceType) {
	v.mem = cacheMemory(v.mem, v.Addr, int(t.Size()))

	var err error
	for _, f := range t.Field {
		switch f.Name {
		case "array":
			var base uint64
			base, err = readUintRaw(v.mem, uintptr(int64(v.Addr)+f.ByteOffset), f.Type.Size())
			if err == nil {
				v.Base = uintptr(base)
				// Dereference array type to get value type
				ptrType, ok := f.Type.(*dwarf.PtrType)
				if !ok {
					v.Unreadable = fmt.Errorf("Invalid type %s in slice array", f.Type)
					return
				}
				v.fieldType = ptrType.Type
			}
		case "len":
			lstrAddr, _ := v.toField(f)
			lstrAddr.loadValue()
			err = lstrAddr.Unreadable
			if err == nil {
				v.Len, _ = constant.Int64Val(lstrAddr.Value)
			}
		case "cap":
			cstrAddr, _ := v.toField(f)
			cstrAddr.loadValue()
			err = cstrAddr.Unreadable
			if err == nil {
				v.Cap, _ = constant.Int64Val(cstrAddr.Value)
			}
		}
		if err != nil {
			v.Unreadable = err
			return
		}
	}

	v.stride = v.fieldType.Size()
	if t, ok := v.fieldType.(*dwarf.PtrType); ok {
		v.stride = t.ByteSize
	}

	return
}

func (v *Variable) loadArrayValues(recurseLevel int) {
	if v.Unreadable != nil {
		return
	}
	if v.Len < 0 {
		v.Unreadable = errors.New("Negative array length")
		return
	}

	count := v.Len
	// Cap number of elements
	if count > maxArrayValues {
		count = maxArrayValues
	}

	if v.stride < maxArrayStridePrefetch {
		v.mem = cacheMemory(v.mem, v.Base, int(v.stride*count))
	}

	errcount := 0

	for i := int64(0); i < count; i++ {
		fieldvar := v.newVariable("", uintptr(int64(v.Base)+(i*v.stride)), v.fieldType)
		fieldvar.loadValueInternal(recurseLevel + 1)

		if fieldvar.Unreadable != nil {
			errcount++
		}

		v.Children = append(v.Children, *fieldvar)
		if errcount > maxErrCount {
			break
		}
	}
}

func (v *Variable) readComplex(size int64) {
	var fs int64
	switch size {
	case 8:
		fs = 4
	case 16:
		fs = 8
	default:
		v.Unreadable = fmt.Errorf("invalid size (%d) for complex type", size)
		return
	}

	ftyp := &dwarf.FloatType{BasicType: dwarf.BasicType{CommonType: dwarf.CommonType{ByteSize: fs, Name: fmt.Sprintf("float%d", fs)}, BitSize: fs * 8, BitOffset: 0}}

	realvar := v.newVariable("real", v.Addr, ftyp)
	imagvar := v.newVariable("imaginary", v.Addr+uintptr(fs), ftyp)
	realvar.loadValue()
	imagvar.loadValue()
	v.Value = constant.BinaryOp(realvar.Value, token.ADD, constant.MakeImag(imagvar.Value))
}

func (v *Variable) writeComplex(real, imag float64, size int64) error {
	err := v.writeFloatRaw(real, int64(size/2))
	if err != nil {
		return err
	}
	imagaddr := *v
	imagaddr.Addr += uintptr(size / 2)
	return imagaddr.writeFloatRaw(imag, int64(size/2))
}

func readIntRaw(mem memoryReadWriter, addr uintptr, size int64) (int64, error) {
	var n int64

	val, err := mem.readMemory(addr, int(size))
	if err != nil {
		return 0, err
	}

	switch size {
	case 1:
		n = int64(val[0])
	case 2:
		n = int64(binary.LittleEndian.Uint16(val))
	case 4:
		n = int64(binary.LittleEndian.Uint32(val))
	case 8:
		n = int64(binary.LittleEndian.Uint64(val))
	}

	return n, nil
}

func (v *Variable) writeUint(value uint64, size int64) error {
	val := make([]byte, size)

	switch size {
	case 1:
		val[0] = byte(value)
	case 2:
		binary.LittleEndian.PutUint16(val, uint16(value))
	case 4:
		binary.LittleEndian.PutUint32(val, uint32(value))
	case 8:
		binary.LittleEndian.PutUint64(val, uint64(value))
	}

	_, err := v.mem.writeMemory(v.Addr, val)
	return err
}

func readUintRaw(mem memoryReadWriter, addr uintptr, size int64) (uint64, error) {
	var n uint64

	val, err := mem.readMemory(addr, int(size))
	if err != nil {
		return 0, err
	}

	switch size {
	case 1:
		n = uint64(val[0])
	case 2:
		n = uint64(binary.LittleEndian.Uint16(val))
	case 4:
		n = uint64(binary.LittleEndian.Uint32(val))
	case 8:
		n = uint64(binary.LittleEndian.Uint64(val))
	}

	return n, nil
}

func (v *Variable) readFloatRaw(size int64) (float64, error) {
	val, err := v.mem.readMemory(v.Addr, int(size))
	if err != nil {
		return 0.0, err
	}
	buf := bytes.NewBuffer(val)

	switch size {
	case 4:
		n := float32(0)
		binary.Read(buf, binary.LittleEndian, &n)
		return float64(n), nil
	case 8:
		n := float64(0)
		binary.Read(buf, binary.LittleEndian, &n)
		return n, nil
	}

	return 0.0, fmt.Errorf("could not read float")
}

func (v *Variable) writeFloatRaw(f float64, size int64) error {
	buf := bytes.NewBuffer(make([]byte, 0, size))

	switch size {
	case 4:
		n := float32(f)
		binary.Write(buf, binary.LittleEndian, n)
	case 8:
		n := float64(f)
		binary.Write(buf, binary.LittleEndian, n)
	}

	_, err := v.mem.writeMemory(v.Addr, buf.Bytes())
	return err
}

func (v *Variable) writeBool(value bool) error {
	val := []byte{0}
	val[0] = *(*byte)(unsafe.Pointer(&value))
	_, err := v.mem.writeMemory(v.Addr, val)
	return err
}

func (v *Variable) readFunctionPtr() {
	val, err := v.mem.readMemory(v.Addr, v.dbp.arch.PtrSize())
	if err != nil {
		v.Unreadable = err
		return
	}

	// dereference pointer to find function pc
	fnaddr := uintptr(binary.LittleEndian.Uint64(val))
	if fnaddr == 0 {
		v.Base = 0
		v.Value = constant.MakeString("")
		return
	}

	val, err = v.mem.readMemory(fnaddr, v.dbp.arch.PtrSize())
	if err != nil {
		v.Unreadable = err
		return
	}

	v.Base = uintptr(binary.LittleEndian.Uint64(val))
	fn := v.dbp.goSymTable.PCToFunc(uint64(v.Base))
	if fn == nil {
		v.Unreadable = fmt.Errorf("could not find function for %#v", v.Base)
		return
	}

	v.Value = constant.MakeString(fn.Name)
}

func (v *Variable) loadMap(recurseLevel int) {
	it := v.mapIterator()
	if it == nil {
		return
	}

	for skip := 0; skip < v.mapSkip; skip++ {
		if ok := it.next(); !ok {
			v.Unreadable = fmt.Errorf("map index out of bounds")
			return
		}
	}

	count := 0
	errcount := 0
	for it.next() {
		if count >= maxArrayValues {
			break
		}
		key := it.key()
		val := it.value()
		key.loadValueInternal(recurseLevel + 1)
		val.loadValueInternal(recurseLevel + 1)
		if key.Unreadable != nil || val.Unreadable != nil {
			errcount++
		}
		v.Children = append(v.Children, *key)
		v.Children = append(v.Children, *val)
		count++
		if errcount > maxErrCount {
			break
		}
	}
}

type mapIterator struct {
	v          *Variable
	numbuckets uint64
	oldmask    uint64
	buckets    *Variable
	oldbuckets *Variable
	b          *Variable
	bidx       uint64

	tophashes *Variable
	keys      *Variable
	values    *Variable
	overflow  *Variable

	idx int64
}

// Code derived from go/src/runtime/hashmap.go
func (v *Variable) mapIterator() *mapIterator {
	sv := v.clone()
	sv.RealType = resolveTypedef(&(sv.RealType.(*dwarf.MapType).TypedefType))
	sv = sv.maybeDereference()
	v.Base = sv.Addr

	maptype, ok := sv.RealType.(*dwarf.StructType)
	if !ok {
		v.Unreadable = fmt.Errorf("wrong real type for map")
		return nil
	}

	it := &mapIterator{v: v, bidx: 0, b: nil, idx: 0}

	if sv.Addr == 0 {
		it.numbuckets = 0
		return it
	}

	v.mem = cacheMemory(v.mem, v.Base, int(v.RealType.Size()))

	for _, f := range maptype.Field {
		var err error
		field, _ := sv.toField(f)
		switch f.Name {
		case "count":
			v.Len, err = field.asInt()
		case "B":
			var b uint64
			b, err = field.asUint()
			it.numbuckets = 1 << b
			it.oldmask = (1 << (b - 1)) - 1
		case "buckets":
			it.buckets = field.maybeDereference()
		case "oldbuckets":
			it.oldbuckets = field.maybeDereference()
		}
		if err != nil {
			v.Unreadable = err
			return nil
		}
	}

	return it
}

func (it *mapIterator) nextBucket() bool {
	if it.overflow != nil && it.overflow.Addr > 0 {
		it.b = it.overflow
	} else {
		it.b = nil

		for it.bidx < it.numbuckets {
			it.b = it.buckets.clone()
			it.b.Addr += uintptr(uint64(it.buckets.DwarfType.Size()) * it.bidx)

			if it.oldbuckets.Addr <= 0 {
				break
			}

			// if oldbuckets is not nil we are iterating through a map that is in
			// the middle of a grow.
			// if the bucket we are looking at hasn't been filled in we iterate
			// instead through its corresponding "oldbucket" (i.e. the bucket the
			// elements of this bucket are coming from) but only if this is the first
			// of the two buckets being created from the same oldbucket (otherwise we
			// would print some keys twice)

			oldbidx := it.bidx & it.oldmask
			oldb := it.oldbuckets.clone()
			oldb.Addr += uintptr(uint64(it.oldbuckets.DwarfType.Size()) * oldbidx)

			if mapEvacuated(oldb) {
				break
			}

			if oldbidx == it.bidx {
				it.b = oldb
				break
			}

			// oldbucket origin for current bucket has not been evacuated but we have already
			// iterated over it so we should just skip it
			it.b = nil
			it.bidx++
		}

		if it.b == nil {
			return false
		}
		it.bidx++
	}

	if it.b.Addr <= 0 {
		return false
	}

	it.b.mem = cacheMemory(it.b.mem, it.b.Addr, int(it.b.RealType.Size()))

	it.tophashes = nil
	it.keys = nil
	it.values = nil
	it.overflow = nil

	for _, f := range it.b.DwarfType.(*dwarf.StructType).Field {
		field, err := it.b.toField(f)
		if err != nil {
			it.v.Unreadable = err
			return false
		}
		if field.Unreadable != nil {
			it.v.Unreadable = field.Unreadable
			return false
		}

		switch f.Name {
		case "tophash":
			it.tophashes = field
		case "keys":
			it.keys = field
		case "values":
			it.values = field
		case "overflow":
			it.overflow = field.maybeDereference()
		}
	}

	// sanity checks
	if it.tophashes == nil || it.keys == nil || it.values == nil {
		it.v.Unreadable = fmt.Errorf("malformed map type")
		return false
	}

	if it.tophashes.Kind != reflect.Array || it.keys.Kind != reflect.Array || it.values.Kind != reflect.Array {
		it.v.Unreadable = fmt.Errorf("malformed map type: keys, values or tophash of a bucket is not an array")
		return false
	}

	if it.tophashes.Len != it.keys.Len || it.tophashes.Len != it.values.Len {
		it.v.Unreadable = fmt.Errorf("malformed map type: inconsistent array length in bucket")
		return false
	}

	return true
}

func (it *mapIterator) next() bool {
	for {
		if it.b == nil || it.idx >= it.tophashes.Len {
			r := it.nextBucket()
			if !r {
				return false
			}
			it.idx = 0
		}
		tophash, _ := it.tophashes.sliceAccess(int(it.idx))
		h, err := tophash.asUint()
		if err != nil {
			it.v.Unreadable = fmt.Errorf("unreadable tophash: %v", err)
			return false
		}
		it.idx++
		if h != hashTophashEmpty {
			return true
		}
	}
}

func (it *mapIterator) key() *Variable {
	k, _ := it.keys.sliceAccess(int(it.idx - 1))
	return k
}

func (it *mapIterator) value() *Variable {
	v, _ := it.values.sliceAccess(int(it.idx - 1))
	return v
}

func mapEvacuated(b *Variable) bool {
	if b.Addr == 0 {
		return true
	}
	for _, f := range b.DwarfType.(*dwarf.StructType).Field {
		if f.Name != "tophash" {
			continue
		}
		tophashes, _ := b.toField(f)
		tophash0var, _ := tophashes.sliceAccess(0)
		tophash0, err := tophash0var.asUint()
		if err != nil {
			return true
		}
		return tophash0 > hashTophashEmpty && tophash0 < hashMinTopHash
	}
	return true
}

func (v *Variable) loadInterface(recurseLevel int, loadData bool) {
	var typestring, data *Variable
	isnil := false

	v.mem = cacheMemory(v.mem, v.Addr, int(v.RealType.Size()))

	ityp := resolveTypedef(&v.RealType.(*dwarf.InterfaceType).TypedefType).(*dwarf.StructType)

	for _, f := range ityp.Field {
		switch f.Name {
		case "tab": // for runtime.iface
			tab, _ := v.toField(f)
			_type, err := tab.structMember("_type")
			if err != nil {
				_, isnil = err.(*IsNilErr)
				if !isnil {
					v.Unreadable = fmt.Errorf("invalid interface type: %v", err)
					return
				}
			} else {
				typestring, err = _type.structMember("_string")
				if err != nil {
					v.Unreadable = fmt.Errorf("invalid interface type: %v", err)
					return
				}
				typestring = typestring.maybeDereference()
			}
		case "_type": // for runtime.eface
			var err error
			_type, _ := v.toField(f)
			typestring, err = _type.structMember("_string")
			if err != nil {
				_, isnil = err.(*IsNilErr)
				if !isnil {
					v.Unreadable = fmt.Errorf("invalid interface type: %v", err)
					return
				}
			} else {
				typestring = typestring.maybeDereference()
			}
		case "data":
			data, _ = v.toField(f)
		}
	}

	if isnil {
		// interface to nil
		data = data.maybeDereference()
		v.Children = []Variable{*data}
		if loadData {
			v.Children[0].loadValueInternal(recurseLevel)
		}
		return
	}

	if typestring == nil || data == nil || typestring.Addr == 0 || typestring.Kind != reflect.String {
		v.Unreadable = fmt.Errorf("invalid interface type")
		return
	}
	typestring.loadValue()
	if typestring.Unreadable != nil {
		v.Unreadable = fmt.Errorf("invalid interface type: %v", typestring.Unreadable)
		return
	}

	t, err := parser.ParseExpr(constant.StringVal(typestring.Value))
	if err != nil {
		v.Unreadable = fmt.Errorf("invalid interface type, unparsable data type: %v", err)
		return
	}

	typ, err := v.dbp.findTypeExpr(t)
	if err != nil {
		v.Unreadable = fmt.Errorf("interface type \"%s\" not found for 0x%x: %v", constant.StringVal(typestring.Value), data.Addr, err)
		return
	}

	realtyp := resolveTypedef(typ)
	if _, isptr := realtyp.(*dwarf.PtrType); !isptr {
		// interface to non-pointer types are pointers even if the type says otherwise
		typ = v.dbp.pointerTo(typ)
	}

	data = data.newVariable("data", data.Addr, typ)

	v.Children = []Variable{*data}
	if loadData {
		v.Children[0].loadValueInternal(recurseLevel)
	}
	return
}

// Fetches all variables of a specific type in the current function scope
func (scope *EvalScope) variablesByTag(tag dwarf.Tag) ([]*Variable, error) {
	reader := scope.DwarfReader()

	_, err := reader.SeekToFunction(scope.PC)
	if err != nil {
		return nil, err
	}

	var vars []*Variable
	for entry, err := reader.NextScopeVariable(); entry != nil; entry, err = reader.NextScopeVariable() {
		if err != nil {
			return nil, err
		}

		if entry.Tag == tag {
			val, err := scope.extractVariableFromEntry(entry)
			if err != nil {
				// skip variables that we can't parse yet
				continue
			}

			vars = append(vars, val)
		}
	}

	return vars, nil
}
