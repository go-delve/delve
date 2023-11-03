package ringbuf

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/cilium/ebpf/internal/unix"
)

type ringbufEventRing struct {
	prod []byte
	cons []byte
	*ringReader
}

func newRingBufEventRing(mapFD, size int) (*ringbufEventRing, error) {
	cons, err := unix.Mmap(mapFD, 0, os.Getpagesize(), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("can't mmap consumer page: %w", err)
	}

	prod, err := unix.Mmap(mapFD, (int64)(os.Getpagesize()), os.Getpagesize()+2*size, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		_ = unix.Munmap(cons)
		return nil, fmt.Errorf("can't mmap data pages: %w", err)
	}

	cons_pos := (*uint64)(unsafe.Pointer(&cons[0]))
	prod_pos := (*uint64)(unsafe.Pointer(&prod[0]))

	ring := &ringbufEventRing{
		prod:       prod,
		cons:       cons,
		ringReader: newRingReader(cons_pos, prod_pos, prod[os.Getpagesize():]),
	}
	runtime.SetFinalizer(ring, (*ringbufEventRing).Close)

	return ring, nil
}

func (ring *ringbufEventRing) Close() {
	runtime.SetFinalizer(ring, nil)

	_ = unix.Munmap(ring.prod)
	_ = unix.Munmap(ring.cons)

	ring.prod = nil
	ring.cons = nil
}

type ringReader struct {
	// These point into mmap'ed memory and must be accessed atomically.
	prod_pos, cons_pos *uint64
	cons               uint64
	mask               uint64
	ring               []byte
}

func newRingReader(cons_ptr, prod_ptr *uint64, ring []byte) *ringReader {
	return &ringReader{
		prod_pos: prod_ptr,
		cons_pos: cons_ptr,
		cons:     atomic.LoadUint64(cons_ptr),
		// cap is always a power of two
		mask: uint64(cap(ring)/2 - 1),
		ring: ring,
	}
}

func (rr *ringReader) loadConsumer() {
	rr.cons = atomic.LoadUint64(rr.cons_pos)
}

func (rr *ringReader) storeConsumer() {
	atomic.StoreUint64(rr.cons_pos, rr.cons)
}

// clamp delta to 'end' if 'start+delta' is beyond 'end'
func clamp(start, end, delta uint64) uint64 {
	if remainder := end - start; delta > remainder {
		return remainder
	}
	return delta
}

func (rr *ringReader) skipRead(skipBytes uint64) {
	rr.cons += clamp(rr.cons, atomic.LoadUint64(rr.prod_pos), skipBytes)
}

func (rr *ringReader) Read(p []byte) (int, error) {
	prod := atomic.LoadUint64(rr.prod_pos)

	n := clamp(rr.cons, prod, uint64(len(p)))

	start := rr.cons & rr.mask

	copy(p, rr.ring[start:start+n])
	rr.cons += n

	if prod == rr.cons {
		return int(n), io.EOF
	}

	return int(n), nil
}
