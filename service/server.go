package service

// Server represents a server for a remote client
// to connect to.
type Server interface {
	Run() error
	Stop(bool) error
}
