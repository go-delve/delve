//go:build !windows

package ringbuf

import (
	"time"

	"github.com/cilium/ebpf/internal/epoll"
	"github.com/cilium/ebpf/internal/unix"
)

var ErrFlushed = epoll.ErrFlushed

var _ poller = (*epollPoller)(nil)

type epollPoller struct {
	*epoll.Poller
	events []unix.EpollEvent
}

func newPoller(fd int) (*epollPoller, error) {
	ep, err := epoll.New()
	if err != nil {
		return nil, err
	}

	if err := ep.Add(fd, 0); err != nil {
		ep.Close()
		return nil, err
	}

	return &epollPoller{
		Poller: ep,
		events: make([]unix.EpollEvent, 1),
	}, nil
}

// Wait blocks until data is available or the deadline is reached.
// Returns [os.ErrDeadlineExceeded] if a deadline was set and no wakeup was received.
// Returns [ErrFlushed] if the ring buffer was flushed manually.
func (p *epollPoller) Wait(deadline time.Time) error {
	_, err := p.Poller.Wait(p.events, deadline)
	return err
}
