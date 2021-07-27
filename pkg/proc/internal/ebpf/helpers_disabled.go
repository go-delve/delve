//go:build !ebpf
// +build !ebpf

package ebpf

// #include "./trace_probe/function_vals.bpf.h"
import "C"
import (
	"errors"
	"unsafe"
)

type EBPFContext struct {
}

func (ctx *EBPFContext) Close() {

}

func (ctx *EBPFContext) AttachUprobe(pid int, name string, offset uint32) error {
	return errors.New("eBPF is disabled")
}

func (ctx *EBPFContext) UpdateArgMap(key, params unsafe.Pointer) error {
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

func CreateFunctionParameterList(entry uint64, args []UProbeArgMap) C.function_parameter_list_t {
	var params C.function_parameter_list_t
	return params
}
