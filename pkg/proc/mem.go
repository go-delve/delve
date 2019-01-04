package proc

import (
	"errors"
	"fmt"

	"github.com/go-delve/delve/pkg/dwarf/op"
)

const cacheEnabled = true

// MemoryReader is like io.ReaderAt, but the offset is a uintptr so that it
// can address all of 64-bit memory.
// Redundant with memoryReadWriter but more easily suited to working with
// the standard io package.
type MemoryReader interface {
	// ReadMemory is just like io.ReaderAt.ReadAt.
	ReadMemory(buf []byte, addr uintptr) (n int, err error)
}

// MemoryReadWriter is an interface for reading or writing to
// the targets memory. This allows us to read from the actual
// target memory or possibly a cache.
type MemoryReadWriter interface {
	MemoryReader
	WriteMemory(addr uintptr, data []byte) (written int, err error)
}

type memCache struct {
	loaded    bool
	cacheAddr uintptr
	cache     []byte
	mem       MemoryReadWriter
}

func (m *memCache) contains(addr uintptr, size int) bool {
	return addr >= m.cacheAddr && addr <= (m.cacheAddr+uintptr(len(m.cache)-size))
}

func (m *memCache) ReadMemory(data []byte, addr uintptr) (n int, err error) {
	if m.contains(addr, len(data)) {
		if !m.loaded {
			_, err := m.mem.ReadMemory(m.cache, m.cacheAddr)
			if err != nil {
				return 0, err
			}
			m.loaded = true
		}
		copy(data, m.cache[addr-m.cacheAddr:])
		return len(data), nil
	}

	return m.mem.ReadMemory(data, addr)
}

func (m *memCache) WriteMemory(addr uintptr, data []byte) (written int, err error) {
	return m.mem.WriteMemory(addr, data)
}

func cacheMemory(mem MemoryReadWriter, addr uintptr, size int) MemoryReadWriter {
	if !cacheEnabled {
		return mem
	}
	if size <= 0 {
		return mem
	}
	switch cacheMem := mem.(type) {
	case *memCache:
		if cacheMem.contains(addr, size) {
			return mem
		}
	case *compositeMemory:
		return mem
	}
	return &memCache{false, addr, make([]byte, size), mem}
}

// fakeAddress used by extractVarInfoFromEntry for variables that do not
// have a memory address, we can't use 0 because a lot of code (likely
// including client code) assumes that addr == 0 is nil
const fakeAddress = 0xbeef0000

// compositeMemory represents a chunk of memory that is stored in CPU
// registers or non-contiguously.
//
// When optimizations are enabled the compiler will store some variables
// into registers and sometimes it will also store structs non-contiguously
// with some fields stored into CPU registers and other fields stored in
// memory.
type compositeMemory struct {
	realmem MemoryReadWriter
	regs    op.DwarfRegisters
	pieces  []op.Piece
	data    []byte
}

func newCompositeMemory(mem MemoryReadWriter, regs op.DwarfRegisters, pieces []op.Piece) (*compositeMemory, error) {
	cmem := &compositeMemory{realmem: mem, regs: regs, pieces: pieces, data: []byte{}}
	for _, piece := range pieces {
		if piece.IsRegister {
			reg := regs.Bytes(piece.RegNum)
			sz := piece.Size
			if sz == 0 && len(pieces) == 1 {
				sz = len(reg)
			}
			if sz > len(reg) {
				return nil, fmt.Errorf("could not read %d bytes from register %d (size: %d)", sz, piece.RegNum, len(reg))
			}
			cmem.data = append(cmem.data, reg[:sz]...)
		} else {
			buf := make([]byte, piece.Size)
			mem.ReadMemory(buf, uintptr(piece.Addr))
			cmem.data = append(cmem.data, buf...)
		}
	}
	return cmem, nil
}

func (mem *compositeMemory) ReadMemory(data []byte, addr uintptr) (int, error) {
	addr -= fakeAddress
	if addr >= uintptr(len(mem.data)) || addr+uintptr(len(data)) > uintptr(len(mem.data)) {
		return 0, errors.New("read out of bounds")
	}
	copy(data, mem.data[addr:addr+uintptr(len(data))])
	return len(data), nil
}

func (mem *compositeMemory) WriteMemory(addr uintptr, data []byte) (int, error) {
	//TODO(aarzilli): implement
	return 0, errors.New("can't write composite memory")
}

// DereferenceMemory returns a MemoryReadWriter that can read and write the
// memory pointed to by pointers in this memory.
// Normally mem and mem.Dereference are the same object, they are different
// only if this MemoryReadWriter is used to access memory outside of the
// normal address space of the inferior process (such as data contained in
// registers, or composite memory).
func DereferenceMemory(mem MemoryReadWriter) MemoryReadWriter {
	switch mem := mem.(type) {
	case *compositeMemory:
		return mem.realmem
	}
	return mem
}

// bufferMemoryReadWriter is dummy a MemoryReadWriter backed by a []byte.
type bufferMemoryReadWriter struct {
	buf []byte
}

func (mem *bufferMemoryReadWriter) ReadMemory(buf []byte, addr uintptr) (n int, err error) {
	copy(buf, mem.buf[addr-fakeAddress:][:len(buf)])
	return len(buf), nil
}

func (mem *bufferMemoryReadWriter) WriteMemory(addr uintptr, data []byte) (written int, err error) {
	copy(mem.buf[addr-fakeAddress:], data)
	return len(data), nil
}
