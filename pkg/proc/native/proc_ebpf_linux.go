//go:build ebpf
// +build ebpf

package native

func (dbp *nativeProcess) SupportsBPF() bool {
	return true
}
