//go:build !ebpf
// +build !ebpf

package ebpf

import (
	"errors"
)

type Uretprobe struct {
	Link   interface{}
	Pid    int
	Path   string
	Offset uint32
}

func (u *Uretprobe) Destroy() error {
	return nil
}

type EBPFContext struct {
}

func (ctx *EBPFContext) GetURetProbes() []Uretprobe {
	return nil
}

func (ctx *EBPFContext) ClearURetProbes() {
}

func (ctx *EBPFContext) GetClearedURetProbes() []Uretprobe {
	return nil
}

func (ctx *EBPFContext) Close() {

}

func (ctx *EBPFContext) AttachUprobe(pid int, name string, offset uint32) error {
	return errors.New("eBPF is disabled")
}

func (ctx *EBPFContext) AttachURetprobe(pid int, name string, offset uint32) error {
	return errors.New("eBPF is disabled")
}

func (ctx *EBPFContext) UpdateArgMap(key uint64, goidOffset int64, args []UProbeArgMap, gAddrOffset uint64) error {
	return errors.New("eBPF is disabled")
}

func (ctx *EBPFContext) GetBufferedTracepoints() []RawUProbeParams {
	return nil
}

func SymbolToOffset(file, symbol string) (uint32, error) {
	return 0, errors.New("eBPF disabled")
}

func LoadEBPFTracingProgram() (*EBPFContext, error) {
	return nil, errors.New("eBPF disabled")
}
