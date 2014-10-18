package proctl

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/dwarf/op"
	"github.com/derekparker/delve/vendor/dwarf"
)

// ThreadContext represents a single thread of execution in the
// traced program.
type ThreadContext struct {
	Id          int
	Process     *DebuggedProcess
	Status      *syscall.WaitStatus
	Regs        *syscall.PtraceRegs
	BreakPoints map[uint64]*BreakPoint
}

// Represents a single breakpoint. Stores information on the break
// point including the byte of data that originally was stored at that
// address.
type BreakPoint struct {
	FunctionName string
	File         string
	Line         int
	Addr         uint64
	OriginalData []byte
}

type Variable struct {
	Name  string
	Value string
	Type  string
}

type BreakPointExistsError struct {
	file string
	line int
	addr uintptr
}

func (bpe BreakPointExistsError) Error() string {
	return fmt.Sprintf("Breakpoint exists at %s:%d at %x", bpe.file, bpe.line, bpe.addr)
}

// Obtains register values from the debugged process.
func (thread *ThreadContext) Registers() (*syscall.PtraceRegs, error) {
	err := syscall.PtraceGetRegs(thread.Id, thread.Regs)
	if err != nil {
		syscall.Tgkill(thread.Process.Pid, thread.Id, syscall.SIGSTOP)
		err = thread.wait()
		if err != nil {
			return nil, err
		}

		err := syscall.PtraceGetRegs(thread.Id, thread.Regs)
		if err != nil {
			return nil, fmt.Errorf("Registers(): %s", err)
		}
	}

	return thread.Regs, nil
}

func (thread *ThreadContext) CurrentPC() (uint64, error) {
	regs, err := thread.Registers()
	if err != nil {
		return 0, err
	}

	return regs.PC(), nil
}

// Sets a breakpoint in the running process.
func (thread *ThreadContext) Break(addr uintptr) (*BreakPoint, error) {
	var (
		int3         = []byte{0xCC}
		f, l, fn     = thread.Process.GoSymTable.PCToLine(uint64(addr))
		originalData = make([]byte, 1)
	)

	if fn == nil {
		return nil, InvalidAddressError{address: addr}
	}

	_, err := syscall.PtracePeekData(thread.Id, addr, originalData)
	if err != nil {
		return nil, err
	}

	if bytes.Equal(originalData, int3) {
		return nil, BreakPointExistsError{f, l, addr}
	}

	_, err = syscall.PtracePokeData(thread.Id, addr, int3)
	if err != nil {
		return nil, err
	}

	breakpoint := &BreakPoint{
		FunctionName: fn.Name,
		File:         f,
		Line:         l,
		Addr:         uint64(addr),
		OriginalData: originalData,
	}

	thread.BreakPoints[uint64(addr)] = breakpoint

	return breakpoint, nil
}

// Clears a breakpoint.
func (thread *ThreadContext) Clear(pc uint64) (*BreakPoint, error) {
	bp, ok := thread.BreakPoints[pc]
	if !ok {
		return nil, fmt.Errorf("No breakpoint currently set for %#v", pc)
	}

	_, err := syscall.PtracePokeData(thread.Id, uintptr(bp.Addr), bp.OriginalData)
	if err != nil {
		return nil, err
	}

	delete(thread.BreakPoints, pc)

	return bp, nil
}

func (thread *ThreadContext) Continue() error {
	// Stepping first will ensure we are able to continue
	// past a breakpoint if that's currently where we are stopped.
	err := thread.Step()
	if err != nil {
		return err
	}

	return syscall.PtraceCont(thread.Id, 0)
}

// Steps through thread of execution.
func (thread *ThreadContext) Step() (err error) {
	regs, err := thread.Registers()
	if err != nil {
		return err
	}

	bp, ok := thread.BreakPoints[regs.PC()-1]
	if ok {
		// Clear the breakpoint so that we can continue execution.
		_, err = thread.Clear(bp.Addr)
		if err != nil {
			return err
		}

		// Reset program counter to our restored instruction.
		regs.SetPC(bp.Addr)
		err = syscall.PtraceSetRegs(thread.Id, regs)
		if err != nil {
			return err
		}

		// Restore breakpoint now that we have passed it.
		defer func() {
			_, err = thread.Break(uintptr(bp.Addr))
		}()
	}

	err = syscall.PtraceSingleStep(thread.Id)
	if err != nil {
		return fmt.Errorf("step failed: ", err.Error())
	}

	_, _, err = wait(thread.Process, thread.Id, 0)
	if err != nil {
		return fmt.Errorf("step failed: ", err.Error())
	}

	return nil
}

// Steps through thread of execution.
func (thread *ThreadContext) Next() (err error) {
	pc, err := thread.CurrentPC()
	if err != nil {
		return err
	}

	if _, ok := thread.BreakPoints[pc-1]; ok {
		// Decrement the PC to be before
		// the breakpoint instruction.
		pc--
	}

	_, l, _ := thread.Process.GoSymTable.PCToLine(pc)
	fde, err := thread.Process.FrameEntries.FDEForPC(pc)
	if err != nil {
		return err
	}

	step := func() (uint64, error) {
		err = thread.Step()
		if err != nil {
			return 0, fmt.Errorf("next stepping failed: ", err.Error())
		}

		return thread.CurrentPC()
	}

	ret := thread.Process.ReturnAddressFromOffset(fde.ReturnAddressOffset(pc))
	for {
		pc, err = step()
		if err != nil {
			return err
		}

		if !fde.Cover(pc) && pc != ret {
			thread.continueToReturnAddress(pc, fde)
			if err != nil {
				if ierr, ok := err.(InvalidAddressError); ok {
					return ierr
				}
			}

			pc, _ = thread.CurrentPC()
		}

		_, nl, _ := thread.Process.GoSymTable.PCToLine(pc)
		if nl != l {
			break
		}
	}

	return nil
}

func (thread *ThreadContext) continueToReturnAddress(pc uint64, fde *frame.FrameDescriptionEntry) error {
	for !fde.Cover(pc) {
		// Our offset here is be 0 because we
		// have stepped into the first instruction
		// of this function. Therefore the function
		// has not had a chance to modify its' stack
		// and change our offset.
		addr := thread.Process.ReturnAddressFromOffset(0)
		bp, err := thread.Break(uintptr(addr))
		if err != nil {
			if _, ok := err.(BreakPointExistsError); !ok {
				return err
			}
		}

		err = thread.Continue()
		if err != nil {
			return err
		}

		err = thread.wait()
		if err != nil {
			return err
		}

		err = thread.clearTempBreakpoint(bp.Addr)
		if err != nil {
			return err
		}

		pc, _ = thread.CurrentPC()
	}

	return nil
}

func (thread *ThreadContext) clearTempBreakpoint(pc uint64) error {
	if bp, ok := thread.BreakPoints[pc]; ok {
		regs, err := thread.Registers()
		if err != nil {
			return err
		}

		// Reset program counter to our restored instruction.
		bp, err = thread.Clear(bp.Addr)
		if err != nil {
			return err
		}

		regs.SetPC(bp.Addr)
		return syscall.PtraceSetRegs(thread.Id, regs)
	}

	return nil
}

func (thread *ThreadContext) wait() error {
	var status syscall.WaitStatus
	_, err := syscall.Wait4(thread.Id, &status, 0, nil)
	if err != nil {
		if status.Exited() {
			delete(thread.Process.Threads, thread.Id)
			return ProcessExitedError{thread.Id}
		}
		return err
	}

	return nil
}

// Returns the value of the named symbol.
func (thread *ThreadContext) EvalSymbol(name string) (*Variable, error) {
	data, err := thread.Process.Executable.DWARF()
	if err != nil {
		return nil, err
	}

	reader := data.Reader()

	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return nil, err
		}

		if entry.Tag != dwarf.TagVariable && entry.Tag != dwarf.TagFormalParameter {
			continue
		}

		n, ok := entry.Val(dwarf.AttrName).(string)
		if !ok || n != name {
			continue
		}

		offset, ok := entry.Val(dwarf.AttrType).(dwarf.Offset)
		if !ok {
			continue
		}

		t, err := data.Type(offset)
		if err != nil {
			return nil, err
		}

		instructions, ok := entry.Val(dwarf.AttrLocation).([]byte)
		if !ok {
			continue
		}

		val, err := thread.extractValue(instructions, 0, t)
		if err != nil {
			return nil, err
		}

		return &Variable{Name: n, Type: t.String(), Value: val}, nil
	}

	return nil, fmt.Errorf("could not find symbol value for %s", name)
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
		offset = int64(regs.Rsp) + offset
	}

	// If we have a user defined type, find the
	// underlying concrete type and use that.
	if tt, ok := typ.(*dwarf.TypedefType); ok {
		typ = tt.Type
	}

	offaddr := uintptr(offset)
	switch t := typ.(type) {
	case *dwarf.PtrType:
		addr, err := thread.readMemory(offaddr, 8)
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
			return thread.readString(offaddr)
		case "[]int":
			return thread.readIntSlice(offaddr)
		default:
			// Here we could recursively call extractValue to grab
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
		return thread.readInt(offaddr)
	case *dwarf.FloatType:
		return thread.readFloat64(offaddr)
	}

	return "", fmt.Errorf("could not find value for type %s", typ)
}

func (thread *ThreadContext) readString(addr uintptr) (string, error) {
	val, err := thread.readMemory(addr, 8)
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
	str := *(*string)(unsafe.Pointer(&val))
	return str, nil
}

func (thread *ThreadContext) readIntSlice(addr uintptr) (string, error) {
	var number uint64

	val, err := thread.readMemory(addr, uintptr(24))
	if err != nil {
		return "", err
	}

	a := binary.LittleEndian.Uint64(val[:8])
	l := binary.LittleEndian.Uint64(val[8:16])
	c := binary.LittleEndian.Uint64(val[16:24])

	val, err = thread.readMemory(uintptr(a), uintptr(8*l))
	if err != nil {
		return "", err
	}

	members := make([]uint64, 0, l)
	buf := bytes.NewBuffer(val)
	for {
		err := binary.Read(buf, binary.LittleEndian, &number)
		if err != nil {
			break
		}

		members = append(members, number)
	}

	str := fmt.Sprintf("len: %d cap: %d %d", l, c, members)

	return str, err
}

func (thread *ThreadContext) readIntArray(addr uintptr, t *dwarf.ArrayType) (string, error) {
	var (
		number  uint64
		members = make([]uint64, 0, t.ByteSize)
	)

	val, err := thread.readMemory(addr, uintptr(t.ByteSize))
	if err != nil {
		return "", err
	}

	buf := bytes.NewBuffer(val)
	for {
		err := binary.Read(buf, binary.LittleEndian, &number)
		if err != nil {
			break
		}

		members = append(members, number)
	}

	str := fmt.Sprintf("[%d]int %d", t.ByteSize/8, members)

	return str, nil
}

func (thread *ThreadContext) readInt(addr uintptr) (string, error) {
	val, err := thread.readMemory(addr, 8)
	if err != nil {
		return "", err
	}

	n := binary.LittleEndian.Uint64(val)

	return strconv.Itoa(int(n)), nil
}

func (thread *ThreadContext) readFloat64(addr uintptr) (string, error) {
	var n float64
	val, err := thread.readMemory(addr, 8)
	if err != nil {
		return "", err
	}
	buf := bytes.NewBuffer(val)
	binary.Read(buf, binary.LittleEndian, &n)

	return strconv.FormatFloat(n, 'f', -1, 64), nil
}

func (thread *ThreadContext) readMemory(addr uintptr, size uintptr) ([]byte, error) {
	buf := make([]byte, size)

	_, err := syscall.PtracePeekData(thread.Id, addr, buf)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func (thread *ThreadContext) handleResult(err error) error {
	if err != nil {
		return err
	}

	_, ps, err := wait(thread.Process, thread.Id, 0)
	if err != nil && err != syscall.ECHILD {
		return err
	}

	if ps != nil {
		thread.Status = ps
		if ps.TrapCause() == -1 && !ps.Exited() {
			regs, err := thread.Registers()
			if err != nil {
				return err
			}

			fmt.Printf("traced program %s at: %#v\n", ps.StopSignal(), regs.PC())
		}
	}

	return nil
}

func threadIds(pid int) []int {
	var threads []int
	dir, err := os.Open(fmt.Sprintf("/proc/%d/task", pid))
	if err != nil {
		panic(err)
	}
	defer dir.Close()

	names, err := dir.Readdirnames(0)
	if err != nil {
		panic(err)
	}

	for _, strid := range names {
		tid, err := strconv.Atoi(strid)
		if err != nil {
			panic(err)
		}

		threads = append(threads, tid)
	}

	return threads
}
