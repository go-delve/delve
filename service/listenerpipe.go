package service

import (
	"errors"
	"net"
	"sync"
)

// ListenerPipe returns a full-duplex in-memory connection, like net.Pipe.
// Unlike net.Pipe one end of the connection is returned as an object
// satisfying the net.Listener interface.
// The first call to the Accept method of this object will return a net.Conn
// connected to the other net.Conn returned by ListenerPipe.
// Any subsequent calls to Accept will block until the listener is closed.
func ListenerPipe() (net.Listener, net.Conn) {
	conn0, conn1 := net.Pipe()
	return &preconnectedListener{conn: conn0, closech: make(chan struct{})}, conn1
}

// preconnectedListener satisfies the net.Listener interface by accepting a
// single pre-established connection.
// The first call to Accept will return the conn field, any subsequent call
// will block until the listener is closed.
type preconnectedListener struct {
	accepted bool
	conn     net.Conn
	closech  chan struct{}
	closeMu  sync.Mutex
	acceptMu sync.Mutex
}

// Accept returns the pre-established connection the first time it's called,
// it blocks until the listener is closed on every subsequent call.
func (l *preconnectedListener) Accept() (net.Conn, error) {
	l.acceptMu.Lock()
	defer l.acceptMu.Unlock()
	if !l.accepted {
		l.accepted = true
		return l.conn, nil
	}
	<-l.closech
	return nil, errors.New("accept failed: listener closed")
}

// Close closes the listener.
func (l *preconnectedListener) Close() error {
	l.closeMu.Lock()
	defer l.closeMu.Unlock()
	if l.closech == nil {
		return nil
	}
	close(l.closech)
	l.closech = nil
	return nil
}

// Addr returns the listener's network address.
func (l *preconnectedListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}
