//go:build !linux || !amd64 || !go1.16 || !cgo
// +build !linux !amd64 !go1.16 !cgo

package native

func (dbp *nativeProcess) SupportsBPF() bool {
	return false
}
