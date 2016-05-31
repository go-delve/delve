package proc

const cacheEnabled = true

type memoryReadWriter interface {
	readMemory(addr uintptr, size int) (data []byte, err error)
	writeMemory(addr uintptr, data []byte) (written int, err error)
}

type memCache struct {
	cacheAddr uintptr
	cache     []byte
	mem       memoryReadWriter
}

func (m *memCache) contains(addr uintptr, size int) bool {
	return addr >= m.cacheAddr && addr <= (m.cacheAddr+uintptr(len(m.cache) - size))
}

func (m *memCache) readMemory(addr uintptr, size int) (data []byte, err error) {
	if m.contains(addr, size) {
		d := make([]byte, size)
		copy(d, m.cache[addr-m.cacheAddr:])
		return d, nil
	}

	return m.mem.readMemory(addr, size)
}

func (m *memCache) writeMemory(addr uintptr, data []byte) (written int, err error) {
	return m.mem.writeMemory(addr, data)
}

func cacheMemory(mem memoryReadWriter, addr uintptr, size int) memoryReadWriter {
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
			cache, err := cacheMem.mem.readMemory(addr, size)
			if err != nil {
				return mem
			}
			return &memCache{addr, cache, mem}
		}
	}
	cache, err := mem.readMemory(addr, size)
	if err != nil {
		return mem
	}
	return &memCache{addr, cache, mem}
}
