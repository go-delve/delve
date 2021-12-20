//go:build linux && amd64 && cgo && go1.16
// +build linux,amd64,cgo,go1.16

package native

func (dbp *nativeProcess) SupportsBPF() bool {
	return true
}
