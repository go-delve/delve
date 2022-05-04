package ringbuf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/internal"
	"github.com/cilium/ebpf/internal/unix"
)

var (
	ErrClosed  = errors.New("ringbuf reader was closed")
	errDiscard = errors.New("sample discarded")
	errBusy    = errors.New("sample not committed yet")
)

func addToEpoll(epollfd, fd int) error {

	event := unix.EpollEvent{
		Events: unix.EPOLLIN,
		Fd:     int32(fd),
	}

	if err := unix.EpollCtl(epollfd, unix.EPOLL_CTL_ADD, fd, &event); err != nil {
		return fmt.Errorf("can't add fd to epoll: %w", err)
	}
	return nil
}

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

func readRecord(rd *ringbufEventRing) (r Record, err error) {
	rd.loadConsumer()
	var header ringbufHeader
	err = binary.Read(rd, internal.NativeEndian, &header)
	if err == io.EOF {
		return Record{}, err
	}

	if err != nil {
		return Record{}, fmt.Errorf("can't read event header: %w", err)
	}

	if header.isBusy() {
		// the next sample in the ring is not committed yet so we
		// exit without storing the reader/consumer position
		// and start again from the same position.
		return Record{}, fmt.Errorf("%w", errBusy)
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

		return Record{}, fmt.Errorf("%w", errDiscard)
	}

	data := make([]byte, dataLenAligned)

	if _, err := io.ReadFull(rd, data); err != nil {
		return Record{}, fmt.Errorf("can't read sample: %w", err)
	}

	rd.storeConsumer()

	return Record{RawSample: data[:header.dataLen()]}, nil
}

// Reader allows reading bpf_ringbuf_output
// from user space.
type Reader struct {
	// mu protects read/write access to the Reader structure
	mu sync.Mutex

	ring *ringbufEventRing

	epollFd     int
	epollEvents []unix.EpollEvent

	closeFd int
	// Ensure we only close once
	closeOnce sync.Once
}

// NewReader creates a new BPF ringbuf reader.
func NewReader(ringbufMap *ebpf.Map) (r *Reader, err error) {
	if ringbufMap.Type() != ebpf.RingBuf {
		return nil, fmt.Errorf("invalid Map type: %s", ringbufMap.Type())
	}

	maxEntries := int(ringbufMap.MaxEntries())
	if maxEntries == 0 || (maxEntries&(maxEntries-1)) != 0 {
		return nil, fmt.Errorf("Ringbuffer map size %d is zero or not a power of two", maxEntries)
	}

	epollFd, err := unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("can't create epoll fd: %w", err)
	}

	var (
		fds  = []int{epollFd}
		ring *ringbufEventRing
	)

	defer func() {
		if err != nil {
			// close epollFd and closeFd
			for _, fd := range fds {
				unix.Close(fd)
			}
			if ring != nil {
				ring.Close()
			}
		}
	}()

	ring, err = newRingBufEventRing(ringbufMap.FD(), maxEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to create ringbuf ring: %w", err)
	}

	if err := addToEpoll(epollFd, ringbufMap.FD()); err != nil {
		return nil, err
	}

	closeFd, err := unix.Eventfd(0, unix.O_CLOEXEC|unix.O_NONBLOCK)
	if err != nil {
		return nil, err
	}
	fds = append(fds, closeFd)

	if err := addToEpoll(epollFd, closeFd); err != nil {
		return nil, err
	}

	r = &Reader{
		ring:    ring,
		epollFd: epollFd,
		// Allocate extra event for closeFd
		epollEvents: make([]unix.EpollEvent, 2),
		closeFd:     closeFd,
	}
	runtime.SetFinalizer(r, (*Reader).Close)
	return r, nil
}

// Close frees resources used by the reader.
//
// It interrupts calls to Read.
func (r *Reader) Close() error {
	var err error
	r.closeOnce.Do(func() {
		runtime.SetFinalizer(r, nil)

		// Interrupt Read() via the closeFd event fd.
		var value [8]byte
		internal.NativeEndian.PutUint64(value[:], 1)

		if _, err = unix.Write(r.closeFd, value[:]); err != nil {
			err = fmt.Errorf("can't write event fd: %w", err)
			return
		}

		// Acquire the lock. This ensures that Read isn't running.
		r.mu.Lock()
		defer r.mu.Unlock()

		unix.Close(r.epollFd)
		unix.Close(r.closeFd)
		r.epollFd, r.closeFd = -1, -1

		if r.ring != nil {
			r.ring.Close()
		}
		r.ring = nil

	})
	if err != nil {
		return fmt.Errorf("close Reader: %w", err)
	}
	return nil
}

// Read the next record from the BPF ringbuf.
//
// Calling Close interrupts the function.
func (r *Reader) Read() (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.epollFd == -1 {
		return Record{}, fmt.Errorf("%w", ErrClosed)
	}

	for {
		nEvents, err := unix.EpollWait(r.epollFd, r.epollEvents, -1)
		if temp, ok := err.(temporaryError); ok && temp.Temporary() {
			// Retry the syscall if we we're interrupted, see https://github.com/golang/go/issues/20400
			continue
		}

		if err != nil {
			return Record{}, err
		}

		for _, event := range r.epollEvents[:nEvents] {
			if int(event.Fd) == r.closeFd {
				return Record{}, fmt.Errorf("%w", ErrClosed)
			}
		}

		record, err := readRecord(r.ring)

		if errors.Is(err, errBusy) || errors.Is(err, errDiscard) {
			continue
		}

		return record, err
	}
}

type temporaryError interface {
	Temporary() bool
}
