package service

// RPCCallback is used by RPC methods to return their result asynchronously.
type RPCCallback interface {
	Return(out interface{}, err error)
}
