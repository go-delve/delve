package ringbuf

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"

	"github.com/cilium/ebpf/internal"
	"github.com/cilium/ebpf/internal/efw"
)

var ErrFlushed = errors.New("ring buffer flushed")

var _ poller = (*windowsPoller)(nil)

type windowsPoller struct {
	closed      atomic.Bool
	handle      windows.Handle
	flushHandle windows.Handle
	handles     []windows.Handle
}

func newPoller(fd int) (*windowsPoller, error) {
	handle, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil, err
	}

	flushHandle, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}

	if err := efw.EbpfMapSetWaitHandle(fd, 0, handle); err != nil {
		windows.CloseHandle(handle)
		windows.CloseHandle(flushHandle)
		return nil, err
	}

	return &windowsPoller{
		handle:      handle,
		flushHandle: flushHandle,
		handles:     []windows.Handle{handle, flushHandle},
	}, nil
}

// Wait blocks until data is available or the deadline is reached.
// Returns [os.ErrDeadlineExceeded] if a deadline was set and no wakeup was received.
// Returns [ErrFlushed] if the ring buffer was flushed manually.
// Returns [os.ErrClosed] if the poller was closed.
func (p *windowsPoller) Wait(deadline time.Time) error {
	if p.closed.Load() {
		return os.ErrClosed
	}

	timeout := uint32(windows.INFINITE)
	if !deadline.IsZero() {
		timeout = uint32(internal.Between(time.Until(deadline).Milliseconds(), 0, windows.INFINITE-1))
	}

	// Wait for either the ring buffer handle or the flush handle to be signaled
	result, err := windows.WaitForMultipleObjects(p.handles, false, timeout)
	switch result {
	case windows.WAIT_OBJECT_0:
		// Ring buffer event
		return nil
	case windows.WAIT_OBJECT_0 + 1:
		if p.closed.Load() {
			return os.ErrClosed
		}
		// Flush event
		return ErrFlushed
	case uint32(windows.WAIT_TIMEOUT):
		return os.ErrDeadlineExceeded
	case windows.WAIT_FAILED:
		return err
	default:
		return fmt.Errorf("unexpected wait result 0x%x: %w", result, err)
	}
}

// Flush interrupts [Wait] with [ErrFlushed].
func (p *windowsPoller) Flush() error {
	// Signal the handle to wake up any waiting threads
	if err := windows.SetEvent(p.flushHandle); err != nil {
		if errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			return os.ErrClosed
		}
		return err
	}

	return nil
}

func (p *windowsPoller) Close() error {
	p.closed.Store(true)

	if err := p.Flush(); err != nil {
		return err
	}

	return errors.Join(windows.CloseHandle(p.handle), windows.CloseHandle(p.flushHandle))
}
