package proc

import (
	"debug/gosym"
	"encoding/binary"
)

type stackLocation struct {
	addr uint64
	file string
	line int
	fn   *gosym.Func
}

// Takes an offset from RSP and returns the address of the
// instruction the currect function is going to return to.
func (thread *Thread) ReturnAddress() (uint64, error) {
	regs, err := thread.Registers()
	if err != nil {
		return 0, err
	}
	locations, err := thread.dbp.stacktrace(regs.PC(), regs.SP(), 1)
	if err != nil {
		return 0, err
	}
	return locations[0].addr, nil
}

type NullAddrError struct{}

func (n NullAddrError) Error() string {
	return "NULL address"
}

func (dbp *DebuggedProcess) stacktrace(pc, sp uint64, depth int) ([]stackLocation, error) {
	var (
		ret       = pc
		data      = make([]byte, 8)
		btoffset  int64
		locations []stackLocation
		retaddr   uintptr
	)
	for i := int64(0); i < int64(depth); i++ {
		fde, err := dbp.frameEntries.FDEForPC(ret)
		if err != nil {
			return nil, err
		}
		btoffset += fde.ReturnAddressOffset(ret)
		retaddr = uintptr(int64(sp) + btoffset + (i * 8))
		if retaddr == 0 {
			return nil, NullAddrError{}
		}
		_, err = readMemory(dbp.CurrentThread, retaddr, data)
		if err != nil {
			return nil, err
		}
		ret = binary.LittleEndian.Uint64(data)
		f, l, fn := dbp.goSymTable.PCToLine(ret)
		locations = append(locations, stackLocation{addr: ret, file: f, line: l, fn: fn})
	}
	return locations, nil
}
