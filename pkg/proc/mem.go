package proc

const cacheEnabled = true

// MemoryReader is like io.ReaderAt, but the offset is a uintptr so that it
// can address all of 64-bit memory.
// Redundant with memoryReadWriter but more easily suited to working with
// the standard io package.
type MemoryReader interface {
	// ReadMemory is just like io.ReaderAt.ReadAt.
	ReadMemory(buf []byte, addr uintptr) (n int, err error)
}

type MemoryReadWriter interface {
	MemoryReader
	WriteMemory(addr uintptr, data []byte) (written int, err error)
}

type memCache struct {
	cacheAddr uintptr
	cache     []byte
	mem       MemoryReadWriter
}

func (m *memCache) contains(addr uintptr, size int) bool {
	return addr >= m.cacheAddr && addr <= (m.cacheAddr+uintptr(len(m.cache)-size))
}

func (m *memCache) ReadMemory(data []byte, addr uintptr) (n int, err error) {
	if m.contains(addr, len(data)) {
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
	if cacheMem, isCache := mem.(*memCache); isCache {
		if cacheMem.contains(addr, size) {
			return mem
		} else {
			cache := make([]byte, size)
			_, err := cacheMem.mem.ReadMemory(cache, addr)
			if err != nil {
				return mem
			}
			return &memCache{addr, cache, mem}
		}
	}
	cache := make([]byte, size)
	_, err := mem.ReadMemory(cache, addr)
	if err != nil {
		return mem
	}
	return &memCache{addr, cache, mem}
}
