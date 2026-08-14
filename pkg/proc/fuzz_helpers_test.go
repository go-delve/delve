package proc

// zeroFillMemory is a MemoryReadWriter that returns zeros on reads.
// Writes succeed and discard bytes unless writeErr is set.
type zeroFillMemory struct {
	writeErr error
}

func (m *zeroFillMemory) ReadMemory(b []byte, addr uint64) (int, error) {
	for i := range b {
		b[i] = 0
	}
	return len(b), nil
}

func (m *zeroFillMemory) WriteMemory(_ uint64, b []byte) (int, error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	return len(b), nil
}
