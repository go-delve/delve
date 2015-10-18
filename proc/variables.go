package proc

import (
	"bytes"
	"debug/dwarf"
	"encoding/binary"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"unsafe"

	"github.com/derekparker/delve/dwarf/op"
	"github.com/derekparker/delve/dwarf/reader"
)

const (
	maxVariableRecurse = 1  // How far to recurse when evaluating nested types.
	maxArrayValues     = 64 // Max value for reading large arrays.
	maxErrCount        = 3  // Max number of read errors to accept while evaluating slices, arrays and structs

	ChanRecv = "chan receive"
	ChanSend = "chan send"
)

// Represents a variable.
type Variable struct {
	Addr      uintptr
	Name      string
	Value     string
	Type      string
	dwarfType dwarf.Type
	thread    *Thread

	Len       int64
	Cap       int64
	base      uintptr
	stride    int64
	fieldType dwarf.Type
}

// Represents a runtime M (OS thread) structure.
type M struct {
	procid   int     // Thread ID or port.
	spinning uint8   // Busy looping.
	blocked  uint8   // Waiting on futex / semaphore.
	curg     uintptr // Current G running on this thread.
}

const (
	// G status, from: src/runtime/runtime2.go
	Gidle            uint64 = iota // 0
	Grunnable                      // 1 runnable and on a run queue
	Grunning                       // 2
	Gsyscall                       // 3
	Gwaiting                       // 4
	Gmoribund_unused               // 5 currently unused, but hardcoded in gdb scripts
	Gdead                          // 6
	Genqueue                       // 7 Only the Gscanenqueue is used.
	Gcopystack                     // 8 in this state when newstack is moving the stack
)

// Represents a runtime G (goroutine) structure (at least the
// fields that Delve is interested in).
type G struct {
	Id         int    // Goroutine ID
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

// Scope for variable evaluation
type EvalScope struct {
	Thread *Thread
	PC     uint64
	CFA    int64
}

func newVariable(name string, addr uintptr, dwarfType dwarf.Type, thread *Thread) (*Variable, error) {
	v := &Variable{
		Name:      name,
		Addr:      addr,
		dwarfType: dwarfType,
		thread:    thread,
		Type:      dwarfType.String(),
	}

	switch t := dwarfType.(type) {
	case *dwarf.StructType:
		if strings.HasPrefix(t.StructName, "[]") {
			err := v.loadSliceInfo(t)
			if err != nil {
				return nil, err
			}
		}
	case *dwarf.ArrayType:
		v.base = v.Addr
		v.Len = t.Count
		v.Cap = -1
		v.fieldType = t.Type
		v.stride = 0

		if t.Count > 0 {
			v.stride = t.ByteSize / t.Count
		}
	}

	return v, nil
}

func (v *Variable) toField(field *dwarf.StructField) (*Variable, error) {
	if v.Addr == 0 {
		return nil, fmt.Errorf("%s is nil", v.Name)
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
	return newVariable(name, uintptr(int64(v.Addr)+field.ByteOffset), field.Type, v.thread)
}

func (scope *EvalScope) DwarfReader() *reader.Reader {
	return scope.Thread.dbp.DwarfReader()
}

func (scope *EvalScope) Type(offset dwarf.Offset) (dwarf.Type, error) {
	return scope.Thread.dbp.dwarf.Type(offset)
}

func (scope *EvalScope) PtrSize() int {
	return scope.Thread.dbp.arch.PtrSize()
}

// Returns whether the goroutine is blocked on
// a channel read operation.
func (g *G) ChanRecvBlocked() bool {
	return g.WaitReason == ChanRecv
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

func parseG(thread *Thread, gaddr uint64, deref bool) (*G, error) {
	initialInstructions := make([]byte, thread.dbp.arch.PtrSize()+1)
	initialInstructions[0] = op.DW_OP_addr
	binary.LittleEndian.PutUint64(initialInstructions[1:], gaddr)
	if deref {
		gaddrbytes, err := thread.readMemory(uintptr(gaddr), thread.dbp.arch.PtrSize())
		if err != nil {
			return nil, fmt.Errorf("error derefing *G %s", err)
		}
		initialInstructions = append([]byte{op.DW_OP_addr}, gaddrbytes...)
		gaddr = binary.LittleEndian.Uint64(gaddrbytes)
		if gaddr == 0 {
			return nil, NoGError{tid: thread.Id}
		}
	}

	rdr := thread.dbp.DwarfReader()
	rdr.Seek(0)
	entry, err := rdr.SeekToTypeNamed("runtime.g")
	if err != nil {
		return nil, err
	}

	// Parse defer
	deferAddr, err := rdr.AddrForMember("_defer", initialInstructions)
	if err != nil {
		return nil, err
	}
	var deferPC uint64
	// Dereference *defer pointer
	deferAddrBytes, err := thread.readMemory(uintptr(deferAddr), thread.dbp.arch.PtrSize())
	if err != nil {
		return nil, fmt.Errorf("error derefing defer %s", err)
	}
	if binary.LittleEndian.Uint64(deferAddrBytes) != 0 {
		initialDeferInstructions := append([]byte{op.DW_OP_addr}, deferAddrBytes...)
		_, err = rdr.SeekToTypeNamed("runtime._defer")
		if err != nil {
			return nil, err
		}
		deferPCAddr, err := rdr.AddrForMember("fn", initialDeferInstructions)
		deferPC, err = thread.readUintRaw(uintptr(deferPCAddr), 8)
		if err != nil {
			return nil, err
		}
		deferPC, err = thread.readUintRaw(uintptr(deferPC), 8)
		if err != nil {
			return nil, err
		}
	}

	// Let's parse all of the members we care about in order so that
	// we don't have to spend any extra time seeking.

	err = rdr.SeekToEntry(entry)
	if err != nil {
		return nil, err
	}

	// Parse sched
	schedAddr, err := rdr.AddrForMember("sched", initialInstructions)
	if err != nil {
		return nil, err
	}
	// From sched, let's parse PC and SP.
	sp, err := thread.readUintRaw(uintptr(schedAddr), 8)
	if err != nil {
		return nil, err
	}
	pc, err := thread.readUintRaw(uintptr(schedAddr+uint64(thread.dbp.arch.PtrSize())), 8)
	if err != nil {
		return nil, err
	}
	// Parse atomicstatus
	atomicStatusAddr, err := rdr.AddrForMember("atomicstatus", initialInstructions)
	if err != nil {
		return nil, err
	}
	atomicStatus, err := thread.readUintRaw(uintptr(atomicStatusAddr), 4)
	// Parse goid
	goidAddr, err := rdr.AddrForMember("goid", initialInstructions)
	if err != nil {
		return nil, err
	}
	goid, err := thread.readIntRaw(uintptr(goidAddr), 8)
	if err != nil {
		return nil, err
	}
	// Parse waitreason
	waitReasonAddr, err := rdr.AddrForMember("waitreason", initialInstructions)
	if err != nil {
		return nil, err
	}
	waitreason, err := thread.readString(uintptr(waitReasonAddr))
	if err != nil {
		return nil, err
	}
	// Parse gopc
	gopcAddr, err := rdr.AddrForMember("gopc", initialInstructions)
	if err != nil {
		return nil, err
	}
	gopc, err := thread.readUintRaw(uintptr(gopcAddr), 8)
	if err != nil {
		return nil, err
	}

	f, l, fn := thread.dbp.goSymTable.PCToLine(pc)
	g := &G{
		Id:         int(goid),
		GoPC:       gopc,
		PC:         pc,
		SP:         sp,
		CurrentLoc: Location{PC: pc, File: f, Line: l, Fn: fn},
		WaitReason: waitreason,
		DeferPC:    deferPC,
		Status:     atomicStatus,
		dbp:        thread.dbp,
	}
	return g, nil
}

// From $GOROOT/src/runtime/traceback.go:597
// isExportedRuntime reports whether name is an exported runtime function.
// It is only for runtime functions, so ASCII A-Z is fine.
func isExportedRuntime(name string) bool {
	const n = len("runtime.")
	return len(name) > n && name[:n] == "runtime." && 'A' <= name[n] && name[n] <= 'Z'
}

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
		name := frame.Call.Fn.Name
		if (strings.Index(name, ".") >= 0) && (!strings.HasPrefix(name, "runtime.") || isExportedRuntime(name)) {
			return frame.Call
		}
	}
	return g.CurrentLoc
}

func (g *G) Go() Location {
	f, l, fn := g.dbp.goSymTable.PCToLine(g.GoPC)
	return Location{PC: g.GoPC, File: f, Line: l, Fn: fn}
}

// Returns information for the named variable.
func (scope *EvalScope) ExtractVariableInfo(name string) (*Variable, error) {
	parts := strings.Split(name, ".")
	varName := parts[0]
	memberNames := parts[1:]

	v, err := scope.extractVarInfo(varName)
	if err != nil {
		origErr := err
		// Attempt to evaluate name as a package variable.
		if len(memberNames) > 0 {
			v, err = scope.packageVarAddr(name)
		} else {
			_, _, fn := scope.Thread.dbp.PCToLine(scope.PC)
			if fn != nil {
				v, err = scope.packageVarAddr(fn.PackageName() + "." + name)
			}
		}
		if err != nil {
			return nil, origErr
		}
		v.Name = name
	} else {
		for _, memberName := range memberNames {
			v, err = v.structMember(memberName)
			if err != nil {
				return nil, err
			}
		}
	}
	return v, nil
}

// Returns the value of the named variable.
func (scope *EvalScope) EvalVariable(name string) (*Variable, error) {
	v, err := scope.ExtractVariableInfo(name)
	if err != nil {
		return nil, err
	}
	err = v.loadValue(true)
	return v, err
}

// Sets the value of the named variable
func (scope *EvalScope) SetVariable(name, value string) error {
	v, err := scope.ExtractVariableInfo(name)
	if err != nil {
		return err
	}
	return v.setValue(value)
}

func (scope *EvalScope) extractVariableFromEntry(entry *dwarf.Entry) (*Variable, error) {
	rdr := scope.DwarfReader()
	v, err := scope.extractVarInfoFromEntry(entry, rdr)
	if err != nil {
		return nil, err
	}
	err = v.loadValue(true)
	return v, err
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
	reader := scope.DwarfReader()

	vars := make([]*Variable, 0)

	for entry, err := reader.NextPackageVariable(); entry != nil; entry, err = reader.NextPackageVariable() {
		if err != nil {
			return nil, err
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

func (dbp *Process) EvalPackageVariable(name string) (*Variable, error) {
	scope := &EvalScope{Thread: dbp.CurrentThread, PC: 0, CFA: 0}

	v, err := scope.packageVarAddr(name)
	if err != nil {
		return nil, err
	}
	err = v.loadValue(true)
	return v, err
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
	structVar, err := v.maybeDereference()
	structVar.Name = v.Name
	if err != nil {
		return nil, err
	}
	structVar = structVar.resolveTypedefs()
	switch t := structVar.dwarfType.(type) {
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
		return nil, fmt.Errorf("%s type %s is not a struct", v.Name, structVar.dwarfType)
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

	return newVariable(n, uintptr(addr), t, scope.Thread)
}

// If v is a pointer a new variable is returned containing the value pointed by v.
func (v *Variable) maybeDereference() (*Variable, error) {
	v = v.resolveTypedefs()

	switch t := v.dwarfType.(type) {
	case *dwarf.PtrType:
		ptrval, err := v.thread.readUintRaw(uintptr(v.Addr), int64(v.thread.dbp.arch.PtrSize()))
		if err != nil {
			return nil, err
		}

		return newVariable("", uintptr(ptrval), t.Type, v.thread)
	default:
		return v, nil
	}
}

// Returns a Variable with the same address but a concrete dwarfType.
func (v *Variable) resolveTypedefs() *Variable {
	typ := v.dwarfType
	for {
		if tt, ok := typ.(*dwarf.TypedefType); ok {
			typ = tt.Type
		} else {
			break
		}
	}
	r := *v
	r.dwarfType = typ
	return &r
}

// Extracts the value of the variable at the given address.
func (v *Variable) loadValue(printStructName bool) (err error) {
	v.Value, err = v.loadValueInternal(printStructName, 0)
	return
}

func (v *Variable) loadValueInternal(printStructName bool, recurseLevel int) (string, error) {
	v = v.resolveTypedefs()

	switch t := v.dwarfType.(type) {
	case *dwarf.PtrType:
		ptrv, err := v.maybeDereference()
		if err != nil {
			return "", err
		}

		if ptrv.Addr == 0 {
			return fmt.Sprintf("%s nil", t.String()), nil
		}

		// Don't increase the recursion level when dereferencing pointers
		val, err := ptrv.loadValueInternal(printStructName, recurseLevel)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("*%s", val), nil
	case *dwarf.StructType:
		switch {
		case t.StructName == "string":
			return v.thread.readString(uintptr(v.Addr))
		case strings.HasPrefix(t.StructName, "[]"):
			return v.loadArrayValues(recurseLevel)
		default:
			// Recursively call extractValue to grab
			// the value of all the members of the struct.
			if recurseLevel <= maxVariableRecurse {
				errcount := 0
				fields := make([]string, 0, len(t.Field))
				for i, field := range t.Field {
					var (
						err      error
						val      string
						fieldvar *Variable
					)

					fieldvar, err = v.toField(field)
					if err == nil {
						val, err = fieldvar.loadValueInternal(printStructName, recurseLevel+1)
					}
					if err != nil {
						errcount++
						val = fmt.Sprintf("<unreadable: %s>", err.Error())
					}

					fields = append(fields, fmt.Sprintf("%s: %s", field.Name, val))

					if errcount > maxErrCount {
						fields = append(fields, fmt.Sprintf("...+%d more", len(t.Field)-i))
					}
				}
				if printStructName {
					return fmt.Sprintf("%s {%s}", t.StructName, strings.Join(fields, ", ")), nil
				}
				return fmt.Sprintf("{%s}", strings.Join(fields, ", ")), nil
			}
			// no fields
			if printStructName {
				return fmt.Sprintf("%s {...}", t.StructName), nil
			}
			return "{...}", nil
		}
	case *dwarf.ArrayType:
		return v.loadArrayValues(recurseLevel)
	case *dwarf.ComplexType:
		return v.readComplex(t.ByteSize)
	case *dwarf.IntType:
		return v.readInt(t.ByteSize)
	case *dwarf.UintType:
		return v.readUint(t.ByteSize)
	case *dwarf.FloatType:
		return v.readFloat(t.ByteSize)
	case *dwarf.BoolType:
		return v.readBool()
	case *dwarf.FuncType:
		return v.readFunctionPtr()
	case *dwarf.VoidType:
		return "(void)", nil
	case *dwarf.UnspecifiedType:
		return "(unknown)", nil
	default:
		fmt.Printf("Unknown type: %T\n", t)
	}

	return "", fmt.Errorf("could not find value for type %s", v.dwarfType)
}

func (v *Variable) setValue(value string) error {
	v = v.resolveTypedefs()

	switch t := v.dwarfType.(type) {
	case *dwarf.PtrType:
		return v.writeUint(false, value, int64(v.thread.dbp.arch.PtrSize()))
	case *dwarf.ComplexType:
		return v.writeComplex(value, t.ByteSize)
	case *dwarf.IntType:
		return v.writeUint(true, value, t.ByteSize)
	case *dwarf.UintType:
		return v.writeUint(false, value, t.ByteSize)
	case *dwarf.FloatType:
		return v.writeFloat(value, t.ByteSize)
	case *dwarf.BoolType:
		return v.writeBool(value)
	default:
		return fmt.Errorf("Can not set value of variables of type: %T\n", t)
	}
}

func (thread *Thread) readString(addr uintptr) (string, error) {
	// string data structure is always two ptrs in size. Addr, followed by len
	// http://research.swtch.com/godata

	// read len
	val, err := thread.readMemory(addr+uintptr(thread.dbp.arch.PtrSize()), thread.dbp.arch.PtrSize())
	if err != nil {
		return "", fmt.Errorf("could not read string len %s", err)
	}
	strlen := int(binary.LittleEndian.Uint64(val))
	if strlen < 0 {
		return "", fmt.Errorf("invalid length: %d", strlen)
	}

	count := strlen
	if count > maxArrayValues {
		count = maxArrayValues
	}

	// read addr
	val, err = thread.readMemory(addr, thread.dbp.arch.PtrSize())
	if err != nil {
		return "", fmt.Errorf("could not read string pointer %s", err)
	}
	addr = uintptr(binary.LittleEndian.Uint64(val))
	if addr == 0 {
		return "", nil
	}

	val, err = thread.readMemory(addr, count)
	if err != nil {
		return "", fmt.Errorf("could not read string at %#v due to %s", addr, err)
	}

	retstr := *(*string)(unsafe.Pointer(&val))

	if count != strlen {
		retstr = retstr + fmt.Sprintf("...+%d more", strlen-count)
	}

	return retstr, nil
}

func (v *Variable) loadSliceInfo(t *dwarf.StructType) error {
	var err error
	for _, f := range t.Field {
		switch f.Name {
		case "array":
			var base uint64
			base, err = v.thread.readUintRaw(uintptr(int64(v.Addr)+f.ByteOffset), int64(v.thread.dbp.arch.PtrSize()))
			if err == nil {
				v.base = uintptr(base)
				// Dereference array type to get value type
				ptrType, ok := f.Type.(*dwarf.PtrType)
				if !ok {
					return fmt.Errorf("Invalid type %s in slice array", f.Type)
				}
				v.fieldType = ptrType.Type
			}
		case "len":
			lstrAddr, err := v.toField(f)
			if err == nil {
				err = lstrAddr.loadValue(true)
			}
			if err == nil {
				v.Len, err = strconv.ParseInt(lstrAddr.Value, 10, 64)
			}
		case "cap":
			cstrAddr, err := v.toField(f)
			if err == nil {
				err = cstrAddr.loadValue(true)
			}
			if err == nil {
				v.Cap, err = strconv.ParseInt(cstrAddr.Value, 10, 64)
			}
		}
	}

	if err != nil {
		return nil
	}

	v.stride = v.fieldType.Size()
	if _, ok := v.fieldType.(*dwarf.PtrType); ok {
		v.stride = int64(v.thread.dbp.arch.PtrSize())
	}

	return nil
}

func (v *Variable) loadArrayValues(recurseLevel int) (string, error) {
	vals := make([]string, 0)
	errcount := 0

	for i := int64(0); i < v.Len; i++ {
		// Cap number of elements
		if i >= maxArrayValues {
			vals = append(vals, fmt.Sprintf("...+%d more", v.Len-maxArrayValues))
			break
		}

		var val string
		fieldvar, err := newVariable("", uintptr(int64(v.base)+(i*v.stride)), v.fieldType, v.thread)
		if err == nil {
			val, err = fieldvar.loadValueInternal(false, recurseLevel+1)
		}
		if err != nil {
			errcount++
			val = fmt.Sprintf("<unreadable: %s>", err.Error())
		}

		vals = append(vals, val)
		if errcount > maxErrCount {
			vals = append(vals, fmt.Sprintf("...+%d more", v.Len-i))
			break
		}
	}

	if v.Cap < 0 {
		return fmt.Sprintf("%s [%s]", v.dwarfType, strings.Join(vals, ",")), nil
	} else {
		return fmt.Sprintf("[]%s len: %d, cap: %d, [%s]", v.fieldType, v.Len, v.Cap, strings.Join(vals, ",")), nil
	}
}

func (v *Variable) readComplex(size int64) (string, error) {
	var fs int64
	switch size {
	case 8:
		fs = 4
	case 16:
		fs = 8
	default:
		return "", fmt.Errorf("invalid size (%d) for complex type", size)
	}
	r, err := v.readFloat(fs)
	if err != nil {
		return "", err
	}
	imagvar := *v
	imagvar.Addr += uintptr(fs)
	i, err := imagvar.readFloat(fs)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(%s + %si)", r, i), nil
}

func (v *Variable) writeComplex(value string, size int64) error {
	var real, imag float64

	expr, err := parser.ParseExpr(value)
	if err != nil {
		return err
	}

	var lits []*ast.BasicLit

	if e, ok := expr.(*ast.ParenExpr); ok {
		expr = e.X
	}

	switch e := expr.(type) {
	case *ast.BinaryExpr: // "<float> + <float>i" or "<float>i + <float>"
		x, xok := e.X.(*ast.BasicLit)
		y, yok := e.Y.(*ast.BasicLit)
		if e.Op != token.ADD || !xok || !yok {
			return fmt.Errorf("Not a complex constant: %s", value)
		}
		lits = []*ast.BasicLit{x, y}
	case *ast.CallExpr: // "complex(<float>, <float>)"
		tname, ok := e.Fun.(*ast.Ident)
		if !ok {
			return fmt.Errorf("Not a complex constant: %s", value)
		}
		if (tname.Name != "complex64") && (tname.Name != "complex128") {
			return fmt.Errorf("Not a complex constant: %s", value)
		}
		if len(e.Args) != 2 {
			return fmt.Errorf("Not a complex constant: %s", value)
		}
		for i := range e.Args {
			lit, ok := e.Args[i].(*ast.BasicLit)
			if !ok {
				return fmt.Errorf("Not a complex constant: %s", value)
			}
			lits = append(lits, lit)
		}
		lits[1].Kind = token.IMAG
		lits[1].Value = lits[1].Value + "i"
	case *ast.BasicLit: // "<float>" or "<float>i"
		lits = []*ast.BasicLit{e}
	default:
		return fmt.Errorf("Not a complex constant: %s", value)
	}

	for _, lit := range lits {
		var err error
		var v float64
		switch lit.Kind {
		case token.FLOAT, token.INT:
			v, err = strconv.ParseFloat(lit.Value, int(size/2))
			real += v
		case token.IMAG:
			v, err = strconv.ParseFloat(lit.Value[:len(lit.Value)-1], int(size/2))
			imag += v
		default:
			return fmt.Errorf("Not a complex constant: %s", value)
		}
		if err != nil {
			return err
		}
	}

	err = v.writeFloatRaw(real, int64(size/2))
	if err != nil {
		return err
	}
	imagaddr := *v
	imagaddr.Addr += uintptr(size / 2)
	return imagaddr.writeFloatRaw(imag, int64(size/2))
}

func (v *Variable) readInt(size int64) (string, error) {
	n, err := v.thread.readIntRaw(v.Addr, size)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(n, 10), nil
}

func (thread *Thread) readIntRaw(addr uintptr, size int64) (int64, error) {
	var n int64

	val, err := thread.readMemory(addr, int(size))
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

func (v *Variable) readUint(size int64) (string, error) {
	n, err := v.thread.readUintRaw(v.Addr, size)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(n, 10), nil
}

func (v *Variable) writeUint(signed bool, value string, size int64) error {
	var (
		n   uint64
		err error
	)
	if signed {
		var m int64
		m, err = strconv.ParseInt(value, 0, int(size*8))
		n = uint64(m)
	} else {
		n, err = strconv.ParseUint(value, 0, int(size*8))
	}
	if err != nil {
		return err
	}

	val := make([]byte, size)

	switch size {
	case 1:
		val[0] = byte(n)
	case 2:
		binary.LittleEndian.PutUint16(val, uint16(n))
	case 4:
		binary.LittleEndian.PutUint32(val, uint32(n))
	case 8:
		binary.LittleEndian.PutUint64(val, uint64(n))
	}

	_, err = v.thread.writeMemory(v.Addr, val)
	return err
}

func (thread *Thread) readUintRaw(addr uintptr, size int64) (uint64, error) {
	var n uint64

	val, err := thread.readMemory(addr, int(size))
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

func (v *Variable) readFloat(size int64) (string, error) {
	val, err := v.thread.readMemory(v.Addr, int(size))
	if err != nil {
		return "", err
	}
	buf := bytes.NewBuffer(val)

	switch size {
	case 4:
		n := float32(0)
		binary.Read(buf, binary.LittleEndian, &n)
		return strconv.FormatFloat(float64(n), 'f', -1, int(size)*8), nil
	case 8:
		n := float64(0)
		binary.Read(buf, binary.LittleEndian, &n)
		return strconv.FormatFloat(n, 'f', -1, int(size)*8), nil
	}

	return "", fmt.Errorf("could not read float")
}

func (v *Variable) writeFloat(value string, size int64) error {
	f, err := strconv.ParseFloat(value, int(size*8))
	if err != nil {
		return err
	}
	return v.writeFloatRaw(f, size)
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

	_, err := v.thread.writeMemory(v.Addr, buf.Bytes())
	return err
}

func (v *Variable) readBool() (string, error) {
	val, err := v.thread.readMemory(v.Addr, 1)
	if err != nil {
		return "", err
	}

	if val[0] == 0 {
		return "false", nil
	}

	return "true", nil
}

func (v *Variable) writeBool(value string) error {
	b, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	val := []byte{0}
	if b {
		val[0] = *(*byte)(unsafe.Pointer(&b))
	}
	_, err = v.thread.writeMemory(v.Addr, val)
	return err
}

func (v *Variable) readFunctionPtr() (string, error) {
	val, err := v.thread.readMemory(v.Addr, v.thread.dbp.arch.PtrSize())
	if err != nil {
		return "", err
	}

	// dereference pointer to find function pc
	fnaddr := uintptr(binary.LittleEndian.Uint64(val))
	if fnaddr == 0 {
		return "nil", nil
	}

	val, err = v.thread.readMemory(fnaddr, v.thread.dbp.arch.PtrSize())
	if err != nil {
		return "", err
	}

	funcAddr := binary.LittleEndian.Uint64(val)
	fn := v.thread.dbp.goSymTable.PCToFunc(uint64(funcAddr))
	if fn == nil {
		return "", fmt.Errorf("could not find function for %#v", funcAddr)
	}

	return fn.Name, nil
}

// Fetches all variables of a specific type in the current function scope
func (scope *EvalScope) variablesByTag(tag dwarf.Tag) ([]*Variable, error) {
	reader := scope.DwarfReader()

	_, err := reader.SeekToFunction(scope.PC)
	if err != nil {
		return nil, err
	}

	vars := make([]*Variable, 0)

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
