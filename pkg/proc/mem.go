package proc

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/go-delve/delve/pkg/dwarf/op"
)

const cacheEnabled = true

// MemoryReader is like io.ReaderAt, but the offset is a uint64 so that it
// can address all of 64-bit memory.
// Redundant with memoryReadWriter but more easily suited to working with
// the standard io package.
type MemoryReader interface {
	// ReadMemory is just like io.ReaderAt.ReadAt.
	ReadMemory(buf []byte, addr uint64) (n int, err error)
}

// MemoryReadWriter is an interface for reading or writing to
// the targets memory. This allows us to read from the actual
// target memory or possibly a cache.
type MemoryReadWriter interface {
	MemoryReader
	WriteMemory(addr uint64, data []byte) (written int, err error)
}

type memCache struct {
	loaded    bool
	cacheAddr uint64
	cache     []byte
	mem       MemoryReadWriter
}

func (m *memCache) contains(addr uint64, size int) bool {
	return addr >= m.cacheAddr && addr <= (m.cacheAddr+uint64(len(m.cache)-size))
}

func (m *memCache) ReadMemory(data []byte, addr uint64) (n int, err error) {
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

func (m *memCache) WriteMemory(addr uint64, data []byte) (written int, err error) {
	return m.mem.WriteMemory(addr, data)
}

func CreateLoadedCachedMemory(data []byte) MemoryReadWriter {
	return &memCache{loaded: true, cacheAddr: fakeAddressUnresolv, cache: data, mem: nil}
}

func cacheMemory(mem MemoryReadWriter, addr uint64, size int) MemoryReadWriter {
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

// compositeMemory represents a chunk of memory that is stored in CPU
// registers or non-contiguously.
//
// When optimizations are enabled the compiler will store some variables
// into registers and sometimes it will also store structs non-contiguously
// with some fields stored into CPU registers and other fields stored in
// memory.
type compositeMemory struct {
	base    uint64 // base address for this composite memory
	realmem MemoryReadWriter
	arch    *Arch
	regs    op.DwarfRegisters
	pieces  []op.Piece
	data    []byte
}

// CreateCompositeMemory created a new composite memory type using the provided MemoryReadWriter as the
// underlying memory buffer.
func CreateCompositeMemory(mem MemoryReadWriter, arch *Arch, regs op.DwarfRegisters, pieces []op.Piece, size int64) (*compositeMemory, error) {
	// This is basically a small wrapper to avoid having to change all callers
	// of newCompositeMemory since it existed first.
	cm, err := newCompositeMemory(mem, arch, regs, pieces, size)
	if cm != nil {
		cm.base = fakeAddressUnresolv
	}
	return cm, err
}

func newCompositeMemory(mem MemoryReadWriter, arch *Arch, regs op.DwarfRegisters, pieces []op.Piece, size int64) (*compositeMemory, error) {
	cmem := &compositeMemory{realmem: mem, arch: arch, regs: regs, pieces: pieces, data: []byte{}}
	for i := range pieces {
		piece := &pieces[i]
		switch piece.Kind {
		case op.RegPiece:
			reg := regs.Bytes(piece.Val)
			if piece.Size == 0 && i == len(pieces)-1 {
				piece.Size = len(reg)
			}
			if piece.Size > len(reg) {
				if regs.FloatLoadError != nil {
					return nil, fmt.Errorf("could not read %d bytes from register %d (size: %d), also error loading floating point registers: %v", piece.Size, piece.Val, len(reg), regs.FloatLoadError)
				}
				return nil, fmt.Errorf("could not read %d bytes from register %d (size: %d)", piece.Size, piece.Val, len(reg))
			}
			cmem.data = append(cmem.data, reg[:piece.Size]...)
		case op.AddrPiece:
			buf := make([]byte, piece.Size)
			mem.ReadMemory(buf, uint64(piece.Val))
			cmem.data = append(cmem.data, buf...)
		case op.ImmPiece:
			buf := piece.Bytes
			if buf == nil {
				sz := 8
				if piece.Size > sz {
					sz = piece.Size
				}
				if piece.Size == 0 && i == len(pieces)-1 {
					piece.Size = arch.PtrSize() // DWARF doesn't say what this should be
				}
				buf = make([]byte, sz)
				binary.LittleEndian.PutUint64(buf, piece.Val)
			}
			cmem.data = append(cmem.data, buf[:piece.Size]...)
		default:
			panic("unsupported piece kind")
		}
	}
	paddingBytes := int(size) - len(cmem.data)
	if paddingBytes > 0 && paddingBytes < arch.ptrSize {
		padding := make([]byte, paddingBytes)
		cmem.data = append(cmem.data, padding...)
	}
	return cmem, nil
}

func (mem *compositeMemory) ReadMemory(data []byte, addr uint64) (int, error) {
	addr -= mem.base
	if addr >= uint64(len(mem.data)) || addr+uint64(len(data)) > uint64(len(mem.data)) {
		return 0, errors.New("read out of bounds")
	}
	copy(data, mem.data[addr:addr+uint64(len(data))])
	return len(data), nil
}

func (mem *compositeMemory) WriteMemory(addr uint64, data []byte) (int, error) {
	addr -= mem.base
	if addr >= uint64(len(mem.data)) || addr+uint64(len(data)) > uint64(len(mem.data)) {
		return 0, errors.New("write out of bounds")
	}
	if mem.regs.ChangeFunc == nil {
		return 0, errors.New("can not write registers")
	}

	copy(mem.data[addr:], data)

	curAddr := uint64(0)
	donesz := 0
	for _, piece := range mem.pieces {
		if curAddr < (addr+uint64(len(data))) && addr < (curAddr+uint64(piece.Size)) {
			// changed memory interval overlaps current piece
			pieceMem := mem.data[curAddr : curAddr+uint64(piece.Size)]

			switch piece.Kind {
			case op.RegPiece:
				oldReg := mem.regs.Reg(piece.Val)
				newReg := op.DwarfRegisterFromBytes(pieceMem)
				err := mem.regs.ChangeFunc(piece.Val, oldReg.Overwrite(newReg))
				if err != nil {
					return donesz, err
				}
			case op.AddrPiece:
				n, err := mem.realmem.WriteMemory(uint64(piece.Val), pieceMem)
				if err != nil {
					return donesz + n, err
				}
			case op.ImmPiece:
				//TODO(aarzilli): maybe return an error if the user tried to change the value?
				// nothing to do
			default:
				panic("unsupported piece kind")
			}
			donesz += piece.Size
		}
		curAddr += uint64(piece.Size)
	}

	return len(data), nil
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
