//go:build !linux || !amd64 || !go1.16

package ebpf

import (
	"debug/elf"
	"errors"
)

type EBPFContext struct {
}

func (ctx *EBPFContext) Close() {

}

func (ctx *EBPFContext) AttachUprobe(pid int, name string, offset uint32) error {
	return errors.New("eBPF is disabled")
}

func (ctx *EBPFContext) AttachURetprobe(pid int, name string, offset uint32) error {
	return errors.New("eBPF is disabled")
}

func (ctx *EBPFContext) UpdateArgMap(key uint64, goidOffset int64, args []UProbeArgMap, gAddrOffset uint64, isret bool) error {
	return errors.New("eBPF is disabled")
}

func (ctx *EBPFContext) GetBufferedTracepoints() []RawUProbeParams {
	return nil
}

func SymbolToOffset(file, symbol string) (uint32, error) {
	return 0, errors.New("eBPF disabled")
}

func LoadEBPFTracingProgram(path string) (*EBPFContext, error) {
	return nil, errors.New("eBPF disabled")
}

func AddressToOffset(f *elf.File, addr uint64) (uint32, error) {
	return 0, errors.New("eBPF disabled")
}
