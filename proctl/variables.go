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
	instructions, ok := entry.Val(dwarf.AttrLocation).([]byte)
	if !ok {
		instructions, ok = entry.Val(dwarf.AttrDataMemberLoc).([]byte)
		if !ok {
			return nil, fmt.Errorf("type assertion failed")
		}
		return instructions, nil
	}
	return instructions, nil
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
	data := thread.Process.Dwarf

	pc, err := thread.CurrentPC()
	if err != nil {
		return nil, err
	}

	fn := thread.Process.GoSymTable.PCToFunc(pc)
	if fn == nil {
		return nil, fmt.Errorf("could not func function scope")
	}

	reader := data.Reader()
	if err = seekToFunctionEntry(fn.Name, reader); err != nil {
		return nil, err
	}

	if strings.Contains(name, ".") {
		idx := strings.Index(name, ".")
		return evaluateStructMember(thread, data, reader, name[:idx], name[idx+1:])
	}

	entry, err := findDwarfEntry(name, reader, false)
	if err != nil {
		return nil, err
	}

	offset, ok := entry.Val(dwarf.AttrType).(dwarf.Offset)
	if !ok {
		return nil, fmt.Errorf("type assertion failed")
	}

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

	return &Variable{Name: name, Type: t.String(), Value: val}, nil
}

// seekToFunctionEntry is basically used to seek the dwarf.Reader to
// the function entry that represents our current scope. From there
// we can find the first child entry that matches the var name and
// use it to determine the value of the variable.
func seekToFunctionEntry(name string, reader *dwarf.Reader) error {
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return err
		}

		if entry.Tag != dwarf.TagSubprogram {
			continue
		}

		n, ok := entry.Val(dwarf.AttrName).(string)
		if !ok {
			continue
		}

		if n == name {
			break
		}
	}

	return nil
}

func findDwarfEntry(name string, reader *dwarf.Reader, member bool) (*dwarf.Entry, error) {
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return nil, err
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

func evaluateStructMember(thread *ThreadContext, data *dwarf.Data, reader *dwarf.Reader, parent, member string) (*Variable, error) {
	parentInstr, err := instructionsFor(parent, thread.Process, reader, false)
	if err != nil {
		return nil, err
	}
	memberInstr, err := instructionsFor(member, thread.Process, reader, true)
	if err != nil {
		return nil, err
	}
	reader.Seek(0)
	entry, err := findDwarfEntry(member, reader, true)
	if err != nil {
		return nil, err
	}
	offset, ok := entry.Val(dwarf.AttrType).(dwarf.Offset)
	if !ok {
		return nil, fmt.Errorf("type assertion failed")
	}
	t, err := data.Type(offset)
	if err != nil {
		return nil, err
	}
	val, err := thread.extractValue(append(parentInstr, memberInstr...), 0, t)
	return &Variable{Name: strings.Join([]string{parent, member}, "."), Type: t.String(), Value: val}, nil
}

// Extracts the value from the instructions given in the DW_AT_location entry.
// We execute the stack program described in the DW_OP_* instruction stream, and
// then grab the value from the other processes memory.
func (thread *ThreadContext) extractValue(instructions []byte, off int64, typ interface{}) (string, error) {
	regs, err := thread.Registers()
	if err != nil {
		return "", err
	}

	fde, err := thread.Process.FrameEntries.FDEForPC(regs.PC())
	if err != nil {
		return "", err
	}

	fctx := fde.EstablishFrame(regs.PC())
	cfaOffset := fctx.CFAOffset()

	offset := off
	if off == 0 {
		offset, err = op.ExecuteStackProgram(cfaOffset, instructions)
		if err != nil {
			return "", err
		}
		offset = int64(regs.SP()) + offset
	}

	// If we have a user defined type, find the
	// underlying concrete type and use that.
	if tt, ok := typ.(*dwarf.TypedefType); ok {
		typ = tt.Type
	}

	offaddr := uintptr(offset)
	switch t := typ.(type) {
	case *dwarf.PtrType:
		addr, err := thread.readMemory(offaddr, ptrsize)
		if err != nil {
			return "", err
		}
		adr := binary.LittleEndian.Uint64(addr)
		val, err := thread.extractValue(nil, int64(adr), t.Type)
		if err != nil {
			return "", err
		}

		retstr := fmt.Sprintf("*%s", val)
		return retstr, nil
	case *dwarf.StructType:
		switch t.StructName {
		case "string":
			return thread.readString(offaddr, t.ByteSize)
		case "[]int":
			return thread.readIntSlice(offaddr, t)
		default:
			// Recursively call extractValue to grab
			// the value of all the members of the struct.
			fields := make([]string, 0, len(t.Field))
			for _, field := range t.Field {
				val, err := thread.extractValue(nil, field.ByteOffset+offset, field.Type)
				if err != nil {
					return "", err
				}

				fields = append(fields, fmt.Sprintf("%s: %s", field.Name, val))
			}
			retstr := fmt.Sprintf("%s {%s}", t.StructName, strings.Join(fields, ", "))
			return retstr, nil
		}
	case *dwarf.ArrayType:
		return thread.readIntArray(offaddr, t)
	case *dwarf.IntType:
		return thread.readInt(offaddr, t.ByteSize)
	case *dwarf.FloatType:
		return thread.readFloat(offaddr, t.ByteSize)
	}

	return "", fmt.Errorf("could not find value for type %s", typ)
}

func (thread *ThreadContext) readString(addr uintptr, size int64) (string, error) {
	val, err := thread.readMemory(addr, uintptr(size))
	if err != nil {
		return "", err
	}

	// deref the pointer to the string
	addr = uintptr(binary.LittleEndian.Uint64(val))
	val, err = thread.readMemory(addr, 16)
	if err != nil {
		return "", err
	}

	i := bytes.IndexByte(val, 0x0)
	val = val[:i]
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
	var n int

	val, err := thread.readMemory(addr, uintptr(size))
	if err != nil {
		return "", err
	}

	switch size {
	case 1:
		n = int(val[0])
	case 2:
		n = int(binary.LittleEndian.Uint16(val))
	case 4:
		n = int(binary.LittleEndian.Uint32(val))
	case 8:
		n = int(binary.LittleEndian.Uint64(val))
	}

	return strconv.Itoa(n), nil
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

func (thread *ThreadContext) readMemory(addr uintptr, size uintptr) ([]byte, error) {
	buf := make([]byte, size)

	_, err := readMemory(thread.Id, addr, buf)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

// Sets the length of a slice.
func setSliceLength(ptr unsafe.Pointer, l int) {
	lptr := (*int)(unsafe.Pointer(uintptr(ptr) + ptrsize))
	*lptr = l
}
