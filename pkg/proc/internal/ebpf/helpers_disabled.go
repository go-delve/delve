//go:build !ebpf
// +build !ebpf

package ebpf

import (
	"errors"
)

type EBPFContext struct {
}

func (ctx *EBPFContext) Close() {

}

func (ctx *EBPFContext) AttachUprobe(pid int, name string, offset uint32) error {
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

func ParseFunctionParameterList(rawParamBytes []byte) RawUProbeParams {
	return RawUProbeParams{}
}
