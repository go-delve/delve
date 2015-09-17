package proc

import (
	"bytes"
	"debug/dwarf"
	"debug/gosym"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/derekparker/delve/dwarf/op"
	"github.com/derekparker/delve/dwarf/reader"
)

const (
	maxVariableRecurse = 1  // How far to recurse when evaluating nested types.
	maxArrayValues     = 64 // Max value for reading large arrays.

	ChanRecv = "chan receive"
	ChanSend = "chan send"
)

// Represents an evaluated variable.
type Variable struct {
	Name  string
	Value string
	Type  string
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

	// Information on goroutine location.
	File string
	Line int
	Func *gosym.Func

	// PC of entry to top-most deferred function.
	DeferPC uint64

	// Thread that this goroutine is currently allocated to
	thread *Thread
}

// Scope for variable evaluation
type EvalScope struct {
	Thread *Thread
	PC     uint64
	CFA    int64
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
	locs, err := dbp.stacktrace(g.PC, g.SP, 4)
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
		File:       f,
		Line:       l,
		Func:       fn,
		WaitReason: waitreason,
		DeferPC:    deferPC,
		Status:     atomicStatus,
	}
	return g, nil
}

// Returns the value of the named variable.
func (scope *EvalScope) EvalVariable(name string) (*Variable, error) {
	reader := scope.DwarfReader()

	_, err := reader.SeekToFunction(scope.PC)
	if err != nil {
		return nil, err
	}

	varName := name
	memberName := ""
	if strings.Contains(name, ".") {
		idx := strings.Index(name, ".")
		varName = name[:idx]
		memberName = name[idx+1:]
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
			if len(memberName) == 0 {
				return scope.extractVariableFromEntry(entry)
			}
			return scope.evaluateStructMember(entry, reader, memberName)
		}
	}

	// Attempt to evaluate name as a package variable.
	if memberName != "" {
		return scope.Thread.dbp.EvalPackageVariable(name)
	} else {
		_, _, fn := scope.Thread.dbp.PCToLine(scope.PC)
		if fn != nil {
			v, err := scope.Thread.dbp.EvalPackageVariable(fn.PackageName() + "." + name)
			if err == nil {
				v.Name = name
				return v, nil
			}
		}
	}

	return nil, fmt.Errorf("could not find symbol value for %s", name)
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
	reader := dbp.DwarfReader()
	scope := &EvalScope{Thread: dbp.CurrentThread, PC: 0, CFA: 0}

	for entry, err := reader.NextPackageVariable(); entry != nil; entry, err = reader.NextPackageVariable() {
		if err != nil {
			return nil, err
		}

		n, ok := entry.Val(dwarf.AttrName).(string)
		if !ok {
			continue
		}

		if n == name {
			return scope.extractVariableFromEntry(entry)
		}
	}

	return nil, fmt.Errorf("could not find symbol value for %s", name)
}

func (scope *EvalScope) evaluateStructMember(parentEntry *dwarf.Entry, rdr *reader.Reader, memberName string) (*Variable, error) {
	parentAddr, err := scope.extractVariableDataAddress(parentEntry, rdr)
	if err != nil {
		return nil, err
	}

	// Get parent variable name
	parentName, ok := parentEntry.Val(dwarf.AttrName).(string)
	if !ok {
		return nil, fmt.Errorf("unable to retrive variable name")
	}

	// Seek reader to the type information so members can be iterated
	_, err = rdr.SeekToType(parentEntry, true, true)
	if err != nil {
		return nil, err
	}

	// Iterate to find member by name
	for memberEntry, err := rdr.NextMemberVariable(); memberEntry != nil; memberEntry, err = rdr.NextMemberVariable() {
		if err != nil {
			return nil, err
		}

		name, ok := memberEntry.Val(dwarf.AttrName).(string)
		if !ok {
			continue
		}

		if name == memberName {
			// Nil ptr, wait until here to throw a nil pointer error to prioritize no such member error
			if parentAddr == 0 {
				return nil, fmt.Errorf("%s is nil", parentName)
			}

			memberInstr, err := rdr.InstructionsForEntry(memberEntry)
			if err != nil {
				return nil, err
			}

			offset, ok := memberEntry.Val(dwarf.AttrType).(dwarf.Offset)
			if !ok {
				return nil, fmt.Errorf("type assertion failed")
			}

			t, err := scope.Type(offset)
			if err != nil {
				return nil, err
			}

			baseAddr := make([]byte, 8)
			binary.LittleEndian.PutUint64(baseAddr, uint64(parentAddr))

			parentInstructions := append([]byte{op.DW_OP_addr}, baseAddr...)
			val, err := scope.extractValue(append(parentInstructions, memberInstr...), 0, t, true)
			if err != nil {
				return nil, err
			}
			return &Variable{Name: strings.Join([]string{parentName, memberName}, "."), Type: t.String(), Value: val}, nil
		}
	}

	return nil, fmt.Errorf("%s has no member %s", parentName, memberName)
}

// Extracts the name, type, and value of a variable from a dwarf entry
func (scope *EvalScope) extractVariableFromEntry(entry *dwarf.Entry) (*Variable, error) {
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

	val, err := scope.extractValue(instructions, 0, t, true)
	if err != nil {
		return nil, err
	}

	return &Variable{Name: n, Type: t.String(), Value: val}, nil
}

// Extracts the address of a variable, dereferencing any pointers
func (scope *EvalScope) extractVariableDataAddress(entry *dwarf.Entry, rdr *reader.Reader) (int64, error) {
	instructions, err := rdr.InstructionsForEntry(entry)
	if err != nil {
		return 0, err
	}

	address, err := op.ExecuteStackProgram(scope.CFA, instructions)
	if err != nil {
		return 0, err
	}

	// Dereference pointers to get down the concrete type
	for typeEntry, err := rdr.SeekToType(entry, true, false); typeEntry != nil; typeEntry, err = rdr.SeekToType(typeEntry, true, false) {
		if err != nil {
			return 0, err
		}

		if typeEntry.Tag != dwarf.TagPointerType {
			break
		}

		ptraddress := uintptr(address)

		ptr, err := scope.Thread.readMemory(ptraddress, scope.PtrSize())
		if err != nil {
			return 0, err
		}
		address = int64(binary.LittleEndian.Uint64(ptr))
	}

	return address, nil
}

// Extracts the value from the instructions given in the DW_AT_location entry.
// We execute the stack program described in the DW_OP_* instruction stream, and
// then grab the value from the other processes memory.
func (scope *EvalScope) extractValue(instructions []byte, addr int64, typ interface{}, printStructName bool) (string, error) {
	return scope.extractValueInternal(instructions, addr, typ, printStructName, 0)
}

func (scope *EvalScope) extractValueInternal(instructions []byte, addr int64, typ interface{}, printStructName bool, recurseLevel int) (string, error) {
	var err error

	if addr == 0 {
		addr, err = op.ExecuteStackProgram(scope.CFA, instructions)
		if err != nil {
			return "", err
		}
	}

	// If we have a user defined type, find the
	// underlying concrete type and use that.
	for {
		if tt, ok := typ.(*dwarf.TypedefType); ok {
			typ = tt.Type
		} else {
			break
		}
	}

	ptraddress := uintptr(addr)
	switch t := typ.(type) {
	case *dwarf.PtrType:
		ptr, err := scope.Thread.readMemory(ptraddress, scope.PtrSize())
		if err != nil {
			return "", err
		}

		intaddr := int64(binary.LittleEndian.Uint64(ptr))
		if intaddr == 0 {
			return fmt.Sprintf("%s nil", t.String()), nil
		}

		// Don't increase the recursion level when dereferencing pointers
		val, err := scope.extractValueInternal(nil, intaddr, t.Type, printStructName, recurseLevel)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("*%s", val), nil
	case *dwarf.StructType:
		switch {
		case t.StructName == "string":
			return scope.Thread.readString(ptraddress)
		case strings.HasPrefix(t.StructName, "[]"):
			return scope.readSlice(ptraddress, t, recurseLevel)
		default:
			// Recursively call extractValue to grab
			// the value of all the members of the struct.
			if recurseLevel <= maxVariableRecurse {
				fields := make([]string, 0, len(t.Field))
				for _, field := range t.Field {
					val, err := scope.extractValueInternal(nil, field.ByteOffset+addr, field.Type, printStructName, recurseLevel+1)
					if err != nil {
						return "", err
					}

					fields = append(fields, fmt.Sprintf("%s: %s", field.Name, val))
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
		return scope.readArray(ptraddress, t, recurseLevel)
	case *dwarf.ComplexType:
		return scope.Thread.readComplex(ptraddress, t.ByteSize)
	case *dwarf.IntType:
		return scope.Thread.readInt(ptraddress, t.ByteSize)
	case *dwarf.UintType:
		return scope.Thread.readUint(ptraddress, t.ByteSize)
	case *dwarf.FloatType:
		return scope.Thread.readFloat(ptraddress, t.ByteSize)
	case *dwarf.BoolType:
		return scope.Thread.readBool(ptraddress)
	case *dwarf.FuncType:
		return scope.Thread.readFunctionPtr(ptraddress)
	case *dwarf.VoidType:
		return "(void)", nil
	case *dwarf.UnspecifiedType:
		return "(unknown)", nil
	default:
		fmt.Printf("Unknown type: %T\n", t)
	}

	return "", fmt.Errorf("could not find value for type %s", typ)
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

	// read addr
	val, err = thread.readMemory(addr, thread.dbp.arch.PtrSize())
	if err != nil {
		return "", fmt.Errorf("could not read string pointer %s", err)
	}
	addr = uintptr(binary.LittleEndian.Uint64(val))
	if addr == 0 {
		return "", nil
	}

	val, err = thread.readMemory(addr, strlen)
	if err != nil {
		return "", fmt.Errorf("could not read string at %#v due to %s", addr, err)
	}

	return *(*string)(unsafe.Pointer(&val)), nil
}

func (scope *EvalScope) readSlice(addr uintptr, t *dwarf.StructType, recurseLevel int) (string, error) {
	var sliceLen, sliceCap int64
	var arrayAddr uintptr
	var arrayType dwarf.Type
	for _, f := range t.Field {
		switch f.Name {
		case "array":
			val, err := scope.Thread.readMemory(addr+uintptr(f.ByteOffset), scope.PtrSize())
			if err != nil {
				return "", err
			}
			arrayAddr = uintptr(binary.LittleEndian.Uint64(val))
			// Dereference array type to get value type
			ptrType, ok := f.Type.(*dwarf.PtrType)
			if !ok {
				return "", fmt.Errorf("Invalid type %s in slice array", f.Type)
			}
			arrayType = ptrType.Type
		case "len":
			lstr, err := scope.extractValue(nil, int64(addr+uintptr(f.ByteOffset)), f.Type, true)
			if err != nil {
				return "", err
			}
			sliceLen, err = strconv.ParseInt(lstr, 10, 64)
			if err != nil {
				return "", err
			}
		case "cap":
			cstr, err := scope.extractValue(nil, int64(addr+uintptr(f.ByteOffset)), f.Type, true)
			if err != nil {
				return "", err
			}
			sliceCap, err = strconv.ParseInt(cstr, 10, 64)
			if err != nil {
				return "", err
			}
		}
	}

	stride := arrayType.Size()
	if _, ok := arrayType.(*dwarf.PtrType); ok {
		stride = int64(scope.PtrSize())
	}
	vals, err := scope.readArrayValues(arrayAddr, sliceLen, stride, arrayType, recurseLevel)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("[]%s len: %d, cap: %d, [%s]", arrayType, sliceLen, sliceCap, strings.Join(vals, ",")), nil
}

func (scope *EvalScope) readArray(addr uintptr, t *dwarf.ArrayType, recurseLevel int) (string, error) {
	if t.Count > 0 {
		vals, err := scope.readArrayValues(addr, t.Count, t.ByteSize/t.Count, t.Type, recurseLevel)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s [%s]", t, strings.Join(vals, ",")), nil
	}
	// because you can declare a zero-size array
	return fmt.Sprintf("%s []", t), nil
}

func (scope *EvalScope) readArrayValues(addr uintptr, count int64, stride int64, t dwarf.Type, recurseLevel int) ([]string, error) {
	vals := make([]string, 0)

	for i := int64(0); i < count; i++ {
		// Cap number of elements
		if i >= maxArrayValues {
			vals = append(vals, fmt.Sprintf("...+%d more", count-maxArrayValues))
			break
		}

		val, err := scope.extractValueInternal(nil, int64(addr+uintptr(i*stride)), t, false, recurseLevel+1)
		if err != nil {
			return nil, err
		}
		vals = append(vals, val)
	}
	return vals, nil
}

func (thread *Thread) readComplex(addr uintptr, size int64) (string, error) {
	var fs int64
	switch size {
	case 8:
		fs = 4
	case 16:
		fs = 8
	default:
		return "", fmt.Errorf("invalid size (%d) for complex type", size)
	}
	r, err := thread.readFloat(addr, fs)
	if err != nil {
		return "", err
	}
	i, err := thread.readFloat(addr+uintptr(fs), fs)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(%s, %si)", r, i), nil
}

func (thread *Thread) readInt(addr uintptr, size int64) (string, error) {
	n, err := thread.readIntRaw(addr, size)
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

func (thread *Thread) readUint(addr uintptr, size int64) (string, error) {
	n, err := thread.readUintRaw(addr, size)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(n, 10), nil
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

func (thread *Thread) readFloat(addr uintptr, size int64) (string, error) {
	val, err := thread.readMemory(addr, int(size))
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

func (thread *Thread) readBool(addr uintptr) (string, error) {
	val, err := thread.readMemory(addr, 1)
	if err != nil {
		return "", err
	}

	if val[0] == 0 {
		return "false", nil
	}

	return "true", nil
}

func (thread *Thread) readFunctionPtr(addr uintptr) (string, error) {
	val, err := thread.readMemory(addr, thread.dbp.arch.PtrSize())
	if err != nil {
		return "", err
	}

	// dereference pointer to find function pc
	addr = uintptr(binary.LittleEndian.Uint64(val))
	if addr == 0 {
		return "nil", nil
	}

	val, err = thread.readMemory(addr, thread.dbp.arch.PtrSize())
	if err != nil {
		return "", err
	}

	funcAddr := binary.LittleEndian.Uint64(val)
	fn := thread.dbp.goSymTable.PCToFunc(uint64(funcAddr))
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
