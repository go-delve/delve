package ringbuf

import (
	"fmt"
	"io"
	"sync/atomic"
	"unsafe"

	"github.com/cilium/ebpf/internal"
	"github.com/cilium/ebpf/internal/sys"
)

type ringReader struct {
	// These point into mmap'ed memory and must be accessed atomically.
	prod_pos, cons_pos *uintptr
	mask               uintptr
	ring               []byte
}

func newRingReader(cons_ptr, prod_ptr *uintptr, ring []byte) *ringReader {
	return &ringReader{
		prod_pos: prod_ptr,
		cons_pos: cons_ptr,
		// cap is always a power of two
		mask: uintptr(cap(ring)/2 - 1),
		ring: ring,
	}
}

// To be able to wrap around data, data pages in ring buffers are mapped twice in
// a single contiguous virtual region.
// Therefore the returned usable size is half the size of the mmaped region.
func (rr *ringReader) size() int {
	return cap(rr.ring) / 2
}

// The amount of data available to read in the ring buffer.
func (rr *ringReader) AvailableBytes() uint64 {
	prod := atomic.LoadUintptr(rr.prod_pos)
	cons := atomic.LoadUintptr(rr.cons_pos)
	return uint64(prod - cons)
}

// Read a record from an event ring.
func (rr *ringReader) readRecord(rec *Record) error {
	prod := atomic.LoadUintptr(rr.prod_pos)
	cons := atomic.LoadUintptr(rr.cons_pos)

	for {
		if remaining := prod - cons; remaining == 0 {
			return errEOR
		} else if remaining < sys.BPF_RINGBUF_HDR_SZ {
			return fmt.Errorf("read record header: %w", io.ErrUnexpectedEOF)
		}

		// read the len field of the header atomically to ensure a happens before
		// relationship with the xchg in the kernel. Without this we may see len
		// without BPF_RINGBUF_BUSY_BIT before the written data is visible.
		// See https://github.com/torvalds/linux/blob/v6.8/kernel/bpf/ringbuf.c#L484
		start := cons & rr.mask
		len := atomic.LoadUint32((*uint32)((unsafe.Pointer)(&rr.ring[start])))
		header := ringbufHeader{Len: len}

		if header.isBusy() {
			// the next sample in the ring is not committed yet so we
			// exit without storing the reader/consumer position
			// and start again from the same position.
			return errBusy
		}

		cons += sys.BPF_RINGBUF_HDR_SZ

		// Data is always padded to 8 byte alignment.
		dataLenAligned := uintptr(internal.Align(header.dataLen(), 8))
		if remaining := prod - cons; remaining < dataLenAligned {
			return fmt.Errorf("read sample data: %w", io.ErrUnexpectedEOF)
		}

		start = cons & rr.mask
		cons += dataLenAligned

		if header.isDiscard() {
			// when the record header indicates that the data should be
			// discarded, we skip it by just updating the consumer position
			// to the next record.
			atomic.StoreUintptr(rr.cons_pos, cons)
			continue
		}

		if n := header.dataLen(); cap(rec.RawSample) < n {
			rec.RawSample = make([]byte, n)
		} else {
			rec.RawSample = rec.RawSample[:n]
		}

		copy(rec.RawSample, rr.ring[start:])
		rec.Remaining = int(prod - cons)
		atomic.StoreUintptr(rr.cons_pos, cons)
		return nil
	}
}
