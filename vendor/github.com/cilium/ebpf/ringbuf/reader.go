package ringbuf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/internal"
	"github.com/cilium/ebpf/internal/epoll"
	"github.com/cilium/ebpf/internal/unix"
)

var (
	ErrClosed  = os.ErrClosed
	errEOR     = errors.New("end of ring")
	errDiscard = errors.New("sample discarded")
	errBusy    = errors.New("sample not committed yet")
)

var ringbufHeaderSize = binary.Size(ringbufHeader{})

// ringbufHeader from 'struct bpf_ringbuf_hdr' in kernel/bpf/ringbuf.c
type ringbufHeader struct {
	Len   uint32
	PgOff uint32
}

func (rh *ringbufHeader) isBusy() bool {
	return rh.Len&unix.BPF_RINGBUF_BUSY_BIT != 0
}

func (rh *ringbufHeader) isDiscard() bool {
	return rh.Len&unix.BPF_RINGBUF_DISCARD_BIT != 0
}

func (rh *ringbufHeader) dataLen() int {
	return int(rh.Len & ^uint32(unix.BPF_RINGBUF_BUSY_BIT|unix.BPF_RINGBUF_DISCARD_BIT))
}

type Record struct {
	RawSample []byte
}

// Read a record from an event ring.
//
// buf must be at least ringbufHeaderSize bytes long.
func readRecord(rd *ringbufEventRing, rec *Record, buf []byte) error {
	rd.loadConsumer()

	buf = buf[:ringbufHeaderSize]
	if _, err := io.ReadFull(rd, buf); err == io.EOF {
		return errEOR
	} else if err != nil {
		return fmt.Errorf("read event header: %w", err)
	}

	header := ringbufHeader{
		internal.NativeEndian.Uint32(buf[0:4]),
		internal.NativeEndian.Uint32(buf[4:8]),
	}

	if header.isBusy() {
		// the next sample in the ring is not committed yet so we
		// exit without storing the reader/consumer position
		// and start again from the same position.
		return errBusy
	}

	/* read up to 8 byte alignment */
	dataLenAligned := uint64(internal.Align(header.dataLen(), 8))

	if header.isDiscard() {
		// when the record header indicates that the data should be
		// discarded, we skip it by just updating the consumer position
		// to the next record instead of normal Read() to avoid allocating data
		// and reading/copying from the ring (which normally keeps track of the
		// consumer position).
		rd.skipRead(dataLenAligned)
		rd.storeConsumer()

		return errDiscard
	}

	if cap(rec.RawSample) < int(dataLenAligned) {
		rec.RawSample = make([]byte, dataLenAligned)
	} else {
		rec.RawSample = rec.RawSample[:dataLenAligned]
	}

	if _, err := io.ReadFull(rd, rec.RawSample); err != nil {
		return fmt.Errorf("read sample: %w", err)
	}

	rd.storeConsumer()
	rec.RawSample = rec.RawSample[:header.dataLen()]
	return nil
}

// Reader allows reading bpf_ringbuf_output
// from user space.
type Reader struct {
	poller *epoll.Poller

	// mu protects read/write access to the Reader structure
	mu          sync.Mutex
	ring        *ringbufEventRing
	epollEvents []unix.EpollEvent
	header      []byte
	haveData    bool
	deadline    time.Time
}

// NewReader creates a new BPF ringbuf reader.
func NewReader(ringbufMap *ebpf.Map) (*Reader, error) {
	if ringbufMap.Type() != ebpf.RingBuf {
		return nil, fmt.Errorf("invalid Map type: %s", ringbufMap.Type())
	}

	maxEntries := int(ringbufMap.MaxEntries())
	if maxEntries == 0 || (maxEntries&(maxEntries-1)) != 0 {
		return nil, fmt.Errorf("ringbuffer map size %d is zero or not a power of two", maxEntries)
	}

	poller, err := epoll.New()
	if err != nil {
		return nil, err
	}

	if err := poller.Add(ringbufMap.FD(), 0); err != nil {
		poller.Close()
		return nil, err
	}

	ring, err := newRingBufEventRing(ringbufMap.FD(), maxEntries)
	if err != nil {
		poller.Close()
		return nil, fmt.Errorf("failed to create ringbuf ring: %w", err)
	}

	return &Reader{
		poller:      poller,
		ring:        ring,
		epollEvents: make([]unix.EpollEvent, 1),
		header:      make([]byte, ringbufHeaderSize),
	}, nil
}

// Close frees resources used by the reader.
//
// It interrupts calls to Read.
func (r *Reader) Close() error {
	if err := r.poller.Close(); err != nil {
		if errors.Is(err, os.ErrClosed) {
			return nil
		}
		return err
	}

	// Acquire the lock. This ensures that Read isn't running.
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ring != nil {
		r.ring.Close()
		r.ring = nil
	}

	return nil
}

// SetDeadline controls how long Read and ReadInto will block waiting for samples.
//
// Passing a zero time.Time will remove the deadline.
func (r *Reader) SetDeadline(t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.deadline = t
}

// Read the next record from the BPF ringbuf.
//
// Returns os.ErrClosed if Close is called on the Reader, or os.ErrDeadlineExceeded
// if a deadline was set.
func (r *Reader) Read() (Record, error) {
	var rec Record
	return rec, r.ReadInto(&rec)
}

// ReadInto is like Read except that it allows reusing Record and associated buffers.
func (r *Reader) ReadInto(rec *Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ring == nil {
		return fmt.Errorf("ringbuffer: %w", ErrClosed)
	}

	for {
		if !r.haveData {
			_, err := r.poller.Wait(r.epollEvents[:cap(r.epollEvents)], r.deadline)
			if err != nil {
				return err
			}
			r.haveData = true
		}

		for {
			err := readRecord(r.ring, rec, r.header)
			if err == errBusy || err == errDiscard {
				continue
			}
			if err == errEOR {
				r.haveData = false
				break
			}

			return err
		}
	}
}
