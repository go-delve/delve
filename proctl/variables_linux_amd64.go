package proctl

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/derekparker/delve/dwarf/op"
	"github.com/derekparker/delve/vendor/dwarf"
)

type Variable struct {
	Name  string
	Value string
	Type  string
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
