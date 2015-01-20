package proctl

import (
	"bytes"
	"debug/dwarf"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/derekparker/delve/dwarf/op"
	"github.com/derekparker/delve/dwarf/reader"
)

type Variable struct {
	Name  string
	Value string
	Type  string
}

type M struct {
	procid   int
	spinning uint8
	blocked  uint8
	curg     uintptr
}

const ptrsize uintptr = unsafe.Sizeof(int(1))

// Parses and returns select info on the internal M
// data structures used by the Go scheduler.
func (thread *ThreadContext) AllM() ([]*M, error) {
	reader := thread.Process.Dwarf.Reader()

	allmaddr, err := parseAllMPtr(thread.Process, reader)
	if err != nil {
		return nil, err
	}
	mptr, err := thread.readMemory(uintptr(allmaddr), ptrsize)
	if err != nil {
		return nil, err
	}
	m := binary.LittleEndian.Uint64(mptr)
	if m == 0 {
		return nil, fmt.Errorf("allm contains no M pointers")
	}

	// parse addresses
	procidInstructions, err := instructionsFor("procid", thread.Process, reader, true)
	if err != nil {
		return nil, err
	}
	spinningInstructions, err := instructionsFor("spinning", thread.Process, reader, true)
	if err != nil {
		return nil, err
	}
	alllinkInstructions, err := instructionsFor("alllink", thread.Process, reader, true)
	if err != nil {
		return nil, err
	}
	blockedInstructions, err := instructionsFor("blocked", thread.Process, reader, true)
	if err != nil {
		return nil, err
	}
	curgInstructions, err := instructionsFor("curg", thread.Process, reader, true)
	if err != nil {
		return nil, err
	}

	var allm []*M
	for {
		// curg
		curgAddr, err := executeMemberStackProgram(mptr, curgInstructions)
		if err != nil {
			return nil, err
		}
		curgBytes, err := thread.readMemory(uintptr(curgAddr), ptrsize)
		if err != nil {
			return nil, fmt.Errorf("could not read curg %#v %s", curgAddr, err)
		}
		curg := binary.LittleEndian.Uint64(curgBytes)

		// procid
		procidAddr, err := executeMemberStackProgram(mptr, procidInstructions)
		if err != nil {
			return nil, err
		}
		procidBytes, err := thread.readMemory(uintptr(procidAddr), ptrsize)
		if err != nil {
			return nil, fmt.Errorf("could not read procid %#v %s", procidAddr, err)
		}
		procid := binary.LittleEndian.Uint64(procidBytes)

		// spinning
		spinningAddr, err := executeMemberStackProgram(mptr, spinningInstructions)
		if err != nil {
			return nil, err
		}
		spinBytes, err := thread.readMemory(uintptr(spinningAddr), 1)
		if err != nil {
			return nil, fmt.Errorf("could not read spinning %#v %s", spinningAddr, err)
		}

		// blocked
		blockedAddr, err := executeMemberStackProgram(mptr, blockedInstructions)
		if err != nil {
			return nil, err
		}
		blockBytes, err := thread.readMemory(uintptr(blockedAddr), 1)
		if err != nil {
			return nil, fmt.Errorf("could not read blocked %#v %s", blockedAddr, err)
		}

		allm = append(allm, &M{
			procid:   int(procid),
			blocked:  blockBytes[0],
			spinning: spinBytes[0],
			curg:     uintptr(curg),
		})

		// Follow the linked list
		alllinkAddr, err := executeMemberStackProgram(mptr, alllinkInstructions)
		if err != nil {
			return nil, err
		}
		mptr, err = thread.readMemory(uintptr(alllinkAddr), ptrsize)
		if err != nil {
			return nil, fmt.Errorf("could not read alllink %#v %s", alllinkAddr, err)
		}
		m = binary.LittleEndian.Uint64(mptr)

		if m == 0 {
			break
		}
	}

	return allm, nil
}

func instructionsFor(name string, dbp *DebuggedProcess, reader *dwarf.Reader, member bool) ([]byte, error) {
	reader.Seek(0)
	entry, err := findDwarfEntry(name, reader, member)
	if err != nil {
		return nil, err
	}
	return instructionsForEntry(entry)
}

func instructionsForEntry(entry *dwarf.Entry) ([]byte, error) {
	if entry.Tag == dwarf.TagMember {
		instructions, ok := entry.Val(dwarf.AttrDataMemberLoc).([]byte)
		if !ok {
			return nil, fmt.Errorf("member data has no data member location attribute")
		}
		// clone slice to prevent stomping on the dwarf data
		return append([]byte{}, instructions...), nil
	}

	// non-member
	instructions, ok := entry.Val(dwarf.AttrLocation).([]byte)
	if !ok {
		return nil, fmt.Errorf("entry has no location attribute")
	}

	// clone slice to prevent stomping on the dwarf data
	return append([]byte{}, instructions...), nil
}

func executeMemberStackProgram(base, instructions []byte) (uint64, error) {
	parentInstructions := append([]byte{op.DW_OP_addr}, base...)
	addr, err := op.ExecuteStackProgram(0, append(parentInstructions, instructions...))
	if err != nil {
		return 0, err
	}

	return uint64(addr), nil
}

func parseAllMPtr(dbp *DebuggedProcess, reader *dwarf.Reader) (uint64, error) {
	entry, err := findDwarfEntry("runtime.allm", reader, false)
	if err != nil {
		return 0, err
	}

	instructions, ok := entry.Val(dwarf.AttrLocation).([]byte)
	if !ok {
		return 0, fmt.Errorf("type assertion failed")
	}
	addr, err := op.ExecuteStackProgram(0, instructions)
	if err != nil {
		return 0, err
	}

	return uint64(addr), nil
}

func (dbp *DebuggedProcess) PrintGoroutinesInfo() error {
	reader := dbp.Dwarf.Reader()

	allglen, err := allglenval(dbp, reader)
	if err != nil {
		return err
	}
	reader.Seek(0)
	allgentryaddr, err := addressFor(dbp, "runtime.allg", reader)
	if err != nil {
		return err
	}
	fmt.Printf("[%d goroutines]\n", allglen)
	faddr, err := dbp.CurrentThread.readMemory(uintptr(allgentryaddr), ptrsize)
	allg := binary.LittleEndian.Uint64(faddr)

	for i := uint64(0); i < allglen; i++ {
		err = printGoroutineInfo(dbp, allg+(i*uint64(ptrsize)), reader)
		if err != nil {
			return err
		}
	}

	return nil
}

func printGoroutineInfo(dbp *DebuggedProcess, addr uint64, reader *dwarf.Reader) error {
	gaddrbytes, err := dbp.CurrentThread.readMemory(uintptr(addr), ptrsize)
	if err != nil {
		return fmt.Errorf("error derefing *G %s", err)
	}
	initialInstructions := append([]byte{op.DW_OP_addr}, gaddrbytes...)

	reader.Seek(0)
	goidaddr, err := offsetFor(dbp, "goid", reader, initialInstructions)
	if err != nil {
		return err
	}
	reader.Seek(0)
	schedaddr, err := offsetFor(dbp, "sched", reader, initialInstructions)
	if err != nil {
		return err
	}

	goidbytes, err := dbp.CurrentThread.readMemory(uintptr(goidaddr), ptrsize)
	if err != nil {
		return fmt.Errorf("error reading goid %s", err)
	}
	schedbytes, err := dbp.CurrentThread.readMemory(uintptr(schedaddr+uint64(ptrsize)), ptrsize)
	if err != nil {
		return fmt.Errorf("error reading sched %s", err)
	}
	gopc := binary.LittleEndian.Uint64(schedbytes)
	f, l, fn := dbp.GoSymTable.PCToLine(gopc)
	fname := ""
	if fn != nil {
		fname = fn.Name
	}
	fmt.Printf("Goroutine %d - %s:%d %s\n", binary.LittleEndian.Uint64(goidbytes), f, l, fname)
	return nil
}

func allglenval(dbp *DebuggedProcess, reader *dwarf.Reader) (uint64, error) {
	entry, err := findDwarfEntry("runtime.allglen", reader, false)
	if err != nil {
		return 0, err
	}

	instructions, ok := entry.Val(dwarf.AttrLocation).([]byte)
	if !ok {
		return 0, fmt.Errorf("type assertion failed")
	}
	addr, err := op.ExecuteStackProgram(0, instructions)
	if err != nil {
		return 0, err
	}
	val, err := dbp.CurrentThread.readMemory(uintptr(addr), 8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(val), nil
}

func addressFor(dbp *DebuggedProcess, name string, reader *dwarf.Reader) (uint64, error) {
	entry, err := findDwarfEntry(name, reader, false)
	if err != nil {
		return 0, err
	}

	instructions, ok := entry.Val(dwarf.AttrLocation).([]byte)
	if !ok {
		return 0, fmt.Errorf("type assertion failed")
	}
	addr, err := op.ExecuteStackProgram(0, instructions)
	if err != nil {
		return 0, err
	}

	return uint64(addr), nil
}

func offsetFor(dbp *DebuggedProcess, name string, reader *dwarf.Reader, parentinstr []byte) (uint64, error) {
	entry, err := findDwarfEntry(name, reader, true)
	if err != nil {
		return 0, err
	}
	instructions, ok := entry.Val(dwarf.AttrDataMemberLoc).([]byte)
	if !ok {
		return 0, fmt.Errorf("type assertion failed")
	}
	offset, err := op.ExecuteStackProgram(0, append(parentinstr, instructions...))
	if err != nil {
		return 0, err
	}

	return uint64(offset), nil
}

// Returns the value of the named symbol.
func (thread *ThreadContext) EvalSymbol(name string) (*Variable, error) {
	pc, err := thread.CurrentPC()
	if err != nil {
		return nil, err
	}

	reader := thread.Process.DwarfReader()

	_, err = reader.SeekToFunction(pc)
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
				return thread.extractVariableFromEntry(entry)
			}
			return thread.evaluateStructMember(entry, reader, memberName)
		}
	}

	return nil, fmt.Errorf("could not find symbol value for %s", name)
}

func findDwarfEntry(name string, reader *dwarf.Reader, member bool) (*dwarf.Entry, error) {
	depth := 1
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return nil, err
		}

		if entry.Children {
			depth++
		}

		if entry.Tag == 0 {
			depth--
			if depth <= 0 {
				return nil, fmt.Errorf("could not find symbol value for %s", name)
			}
		}

		if member {
			if entry.Tag != dwarf.TagMember {
				continue
			}
		} else {
			if entry.Tag != dwarf.TagVariable && entry.Tag != dwarf.TagFormalParameter && entry.Tag != dwarf.TagStructType {
				continue
			}
		}

		n, ok := entry.Val(dwarf.AttrName).(string)
		if !ok || n != name {
			continue
		}
		return entry, nil
	}
	return nil, fmt.Errorf("could not find symbol value for %s", name)
}

func (thread *ThreadContext) evaluateStructMember(parentEntry *dwarf.Entry, reader *reader.Reader, memberName string) (*Variable, error) {
	parentAddr, err := thread.extractVariableDataAddress(parentEntry, reader)
	if err != nil {
		return nil, err
	}

	// Get parent variable name
	parentName, ok := parentEntry.Val(dwarf.AttrName).(string)
	if !ok {
		return nil, fmt.Errorf("unable to retrive variable name")
	}

	// Seek reader to the type information so members can be iterated
	_, err = reader.SeekToType(parentEntry, true, true)
	if err != nil {
		return nil, err
	}

	// Iterate to find member by name
	for memberEntry, err := reader.NextMemberVariable(); memberEntry != nil; memberEntry, err = reader.NextMemberVariable() {
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

			memberInstr, err := instructionsForEntry(memberEntry)
			if err != nil {
				return nil, err
			}

			offset, ok := memberEntry.Val(dwarf.AttrType).(dwarf.Offset)
			if !ok {
				return nil, fmt.Errorf("type assertion failed")
			}

			data := thread.Process.Dwarf
			t, err := data.Type(offset)
			if err != nil {
				return nil, err
			}

			baseAddr := make([]byte, 8)
			binary.LittleEndian.PutUint64(baseAddr, uint64(parentAddr))

			parentInstructions := append([]byte{op.DW_OP_addr}, baseAddr...)
			val, err := thread.extractValue(append(parentInstructions, memberInstr...), 0, t)
			if err != nil {
				return nil, err
			}
			return &Variable{Name: strings.Join([]string{parentName, memberName}, "."), Type: t.String(), Value: val}, nil
		}
	}

	return nil, fmt.Errorf("%s has no member %s", parentName, memberName)
}

// Extracts the name, type, and value of a variable from a dwarf entry
func (thread *ThreadContext) extractVariableFromEntry(entry *dwarf.Entry) (*Variable, error) {
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

	data := thread.Process.Dwarf
	t, err := data.Type(offset)
	if err != nil {
		return nil, err
	}

	instructions, ok := entry.Val(dwarf.AttrLocation).([]byte)
	if !ok {
		return nil, fmt.Errorf("type assertion failed")
	}

	val, err := thread.extractValue(instructions, 0, t)
	if err != nil {
		return nil, err
	}

	return &Variable{Name: n, Type: t.String(), Value: val}, nil
}

// Execute the stack program taking into account the current stack frame
func (thread *ThreadContext) executeStackProgram(instructions []byte) (int64, error) {
	regs, err := thread.Registers()
	if err != nil {
		return 0, err
	}

	fde, err := thread.Process.FrameEntries.FDEForPC(regs.PC())
	if err != nil {
		return 0, err
	}

	fctx := fde.EstablishFrame(regs.PC())
	cfa := fctx.CFAOffset() + int64(regs.SP())
	address, err := op.ExecuteStackProgram(cfa, instructions)
	if err != nil {
		return 0, err
	}
	return address, nil
}

// Extracts the address of a variable, dereferencing any pointers
func (thread *ThreadContext) extractVariableDataAddress(entry *dwarf.Entry, reader *reader.Reader) (int64, error) {
	instructions, err := instructionsForEntry(entry)
	if err != nil {
		return 0, err
	}

	address, err := thread.executeStackProgram(instructions)
	if err != nil {
		return 0, err
	}

	// Dereference pointers to get down the concrete type
	for typeEntry, err := reader.SeekToType(entry, true, false); typeEntry != nil; typeEntry, err = reader.SeekToType(typeEntry, true, false) {
		if err != nil {
			return 0, err
		}

		if typeEntry.Tag != dwarf.TagPointerType {
			break
		}

		ptraddress := uintptr(address)

		ptr, err := thread.readMemory(ptraddress, ptrsize)
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
func (thread *ThreadContext) extractValue(instructions []byte, addr int64, typ interface{}) (string, error) {
	var err error

	if addr == 0 {
		addr, err = thread.executeStackProgram(instructions)
		if err != nil {
			return "", err
		}
	}

	// If we have a user defined type, find the
	// underlying concrete type and use that.
	if tt, ok := typ.(*dwarf.TypedefType); ok {
		typ = tt.Type
	}

	ptraddress := uintptr(addr)
	switch t := typ.(type) {
	case *dwarf.PtrType:
		ptr, err := thread.readMemory(ptraddress, ptrsize)
		if err != nil {
			return "", err
		}

		intaddr := int64(binary.LittleEndian.Uint64(ptr))
		if intaddr == 0 {
			return fmt.Sprintf("%s nil", t.String()), nil
		}

		val, err := thread.extractValue(nil, intaddr, t.Type)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("*%s", val), nil
	case *dwarf.StructType:
		switch t.StructName {
		case "string":
			return thread.readString(ptraddress)
		case "[]int":
			return thread.readIntSlice(ptraddress, t)
		default:
			// Recursively call extractValue to grab
			// the value of all the members of the struct.
			fields := make([]string, 0, len(t.Field))
			for _, field := range t.Field {
				val, err := thread.extractValue(nil, field.ByteOffset+addr, field.Type)
				if err != nil {
					return "", err
				}

				fields = append(fields, fmt.Sprintf("%s: %s", field.Name, val))
			}
			retstr := fmt.Sprintf("%s {%s}", t.StructName, strings.Join(fields, ", "))
			return retstr, nil
		}
	case *dwarf.ArrayType:
		return thread.readIntArray(ptraddress, t)
	case *dwarf.IntType:
		return thread.readInt(ptraddress, t.ByteSize)
	case *dwarf.UintType:
		return thread.readUint(ptraddress, t.ByteSize)
	case *dwarf.FloatType:
		return thread.readFloat(ptraddress, t.ByteSize)
	case *dwarf.BoolType:
		return thread.readBool(ptraddress)
	}

	return "", fmt.Errorf("could not find value for type %s", typ)
}

func (thread *ThreadContext) readString(addr uintptr) (string, error) {
	// string data structure is always two ptrs in size. Addr, followed by len
	// http://research.swtch.com/godata

	// read len
	val, err := thread.readMemory(addr+ptrsize, ptrsize)
	if err != nil {
		return "", err
	}
	strlen := uintptr(binary.LittleEndian.Uint64(val))

	// read addr
	val, err = thread.readMemory(addr, ptrsize)
	if err != nil {
		return "", err
	}
	addr = uintptr(binary.LittleEndian.Uint64(val))

	val, err = thread.readMemory(addr, strlen)
	if err != nil {
		return "", err
	}

	return *(*string)(unsafe.Pointer(&val)), nil
}

func (thread *ThreadContext) readIntSlice(addr uintptr, t *dwarf.StructType) (string, error) {
	val, err := thread.readMemory(addr, uintptr(24))
	if err != nil {
		return "", err
	}

	a := binary.LittleEndian.Uint64(val[:8])
	l := binary.LittleEndian.Uint64(val[8:16])
	c := binary.LittleEndian.Uint64(val[16:24])

	val, err = thread.readMemory(uintptr(a), uintptr(uint64(ptrsize)*l))
	if err != nil {
		return "", err
	}

	switch t.StructName {
	case "[]int":
		members := *(*[]int)(unsafe.Pointer(&val))
		setSliceLength(unsafe.Pointer(&members), int(l))
		return fmt.Sprintf("len: %d cap: %d %d", l, c, members), nil
	}
	return "", fmt.Errorf("Could not read slice")
}

func (thread *ThreadContext) readIntArray(addr uintptr, t *dwarf.ArrayType) (string, error) {
	val, err := thread.readMemory(addr, uintptr(t.ByteSize))
	if err != nil {
		return "", err
	}

	switch t.Type.Size() {
	case 4:
		members := *(*[]uint32)(unsafe.Pointer(&val))
		setSliceLength(unsafe.Pointer(&members), int(t.Count))
		return fmt.Sprintf("%s %d", t, members), nil
	case 8:
		members := *(*[]uint64)(unsafe.Pointer(&val))
		setSliceLength(unsafe.Pointer(&members), int(t.Count))
		return fmt.Sprintf("%s %d", t, members), nil
	}
	return "", fmt.Errorf("Could not read array")
}

func (thread *ThreadContext) readInt(addr uintptr, size int64) (string, error) {
	var n int64

	val, err := thread.readMemory(addr, uintptr(size))
	if err != nil {
		return "", err
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

	return strconv.FormatInt(n, 10), nil
}

func (thread *ThreadContext) readUint(addr uintptr, size int64) (string, error) {
	var n uint64

	val, err := thread.readMemory(addr, uintptr(size))
	if err != nil {
		return "", err
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

	return strconv.FormatUint(n, 10), nil
}

func (thread *ThreadContext) readFloat(addr uintptr, size int64) (string, error) {
	val, err := thread.readMemory(addr, uintptr(size))
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

func (thread *ThreadContext) readBool(addr uintptr) (string, error) {
	val, err := thread.readMemory(addr, uintptr(1))
	if err != nil {
		return "", err
	}

	if val[0] == 0 {
		return "false", nil
	}

	return "true", nil
}

func (thread *ThreadContext) readMemory(addr uintptr, size uintptr) ([]byte, error) {
	buf := make([]byte, size)

	_, err := readMemory(thread.Id, addr, buf)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

// Fetches all variables of a specific type in the current function scope
func (thread *ThreadContext) variablesByTag(tag dwarf.Tag) ([]*Variable, error) {
	pc, err := thread.CurrentPC()
	if err != nil {
		return nil, err
	}

	reader := thread.Process.DwarfReader()

	_, err = reader.SeekToFunction(pc)
	if err != nil {
		return nil, err
	}

	vars := make([]*Variable, 0)

	for entry, err := reader.NextScopeVariable(); entry != nil; entry, err = reader.NextScopeVariable() {
		if err != nil {
			return nil, err
		}

		if entry.Tag == tag {
			val, err := thread.extractVariableFromEntry(entry)
			if err != nil {
				// skip variables that we can't parse yet
				continue
			}

			vars = append(vars, val)
		}
	}

	return vars, nil
}

// LocalVariables returns all local variables from the current function scope
func (thread *ThreadContext) LocalVariables() ([]*Variable, error) {
	return thread.variablesByTag(dwarf.TagVariable)
}

// FunctionArguments returns the name, value, and type of all current function arguments
func (thread *ThreadContext) FunctionArguments() ([]*Variable, error) {
	return thread.variablesByTag(dwarf.TagFormalParameter)
}

// Sets the length of a slice.
func setSliceLength(ptr unsafe.Pointer, l int) {
	lptr := (*int)(unsafe.Pointer(uintptr(ptr) + ptrsize))
	*lptr = l
}
