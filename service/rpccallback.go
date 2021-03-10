package service

// RPCCallback is used by RPC methods to return their result asynchronously.
type RPCCallback interface {
	Return(out interface{}, err error)

	// SetupDone returns a channel that should be closed to signal that the
	// asynchornous method has completed setup and the server is ready to
	// receive other requests.
	SetupDoneChan() chan struct{}
}
