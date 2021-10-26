//go:build ebpf
// +build ebpf

package ebpf

// #include "./trace_probe/function_vals.bpf.h"
import "C"
import (
	"debug/elf"
	_ "embed"
	"encoding/binary"
	"errors"
	"reflect"
	"runtime"
	"sync"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"

	bpf "github.com/aquasecurity/libbpfgo"
	"github.com/aquasecurity/libbpfgo/helpers"
)

//go:embed trace_probe/trace.o
var TraceProbeBytes []byte

const FakeAddressBase = 0xbeed000000000000

type EBPFContext struct {
	bpfModule  *bpf.Module
	bpfProg    *bpf.BPFProg
	bpfEvents  chan []byte
	bpfRingBuf *bpf.RingBuffer
	bpfArgMap  *bpf.BPFMap

	parsedBpfEvents []RawUProbeParams
	m               sync.Mutex
}

func (ctx *EBPFContext) Close() {
	if ctx.bpfModule != nil {
		ctx.bpfModule.Close()
	}
}

func (ctx *EBPFContext) AttachUprobe(pid int, name string, offset uint32) error {
	if ctx.bpfProg == nil {
		return errors.New("no eBPF program loaded")
	}
	_, err := ctx.bpfProg.AttachUprobe(pid, name, offset)
	return err
}

func (ctx *EBPFContext) UpdateArgMap(key uint64, goidOffset int64, args []UProbeArgMap, gAddrOffset uint64, isret bool) error {
	if ctx.bpfArgMap == nil {
		return errors.New("eBPF map not loaded")
	}
	params := createFunctionParameterList(key, goidOffset, args, isret)
	params.g_addr_offset = C.longlong(gAddrOffset)
	return ctx.bpfArgMap.Update(unsafe.Pointer(&key), unsafe.Pointer(&params))
}

func (ctx *EBPFContext) GetBufferedTracepoints() []RawUProbeParams {
	ctx.m.Lock()
	defer ctx.m.Unlock()

	if len(ctx.parsedBpfEvents) == 0 {
		return make([]RawUProbeParams, 0)
	}

	events := make([]RawUProbeParams, len(ctx.parsedBpfEvents))
	copy(events, ctx.parsedBpfEvents)
	ctx.parsedBpfEvents = ctx.parsedBpfEvents[:0]
	return events
}

func SymbolToOffset(file, symbol string) (uint32, error) {
	return helpers.SymbolToOffset(file, symbol)
}

func LoadEBPFTracingProgram() (*EBPFContext, error) {
	var ctx EBPFContext
	var err error

	ctx.bpfModule, err = bpf.NewModuleFromBuffer(TraceProbeBytes, "trace_probe/trace.o")
	if err != nil {
		return nil, err
	}

	ctx.bpfModule.BPFLoadObject()
	prog, err := ctx.bpfModule.GetProgram("uprobe__dlv_trace")
	if err != nil {
		return nil, err
	}
	ctx.bpfProg = prog

	ctx.bpfEvents = make(chan []byte)
	ctx.bpfRingBuf, err = ctx.bpfModule.InitRingBuf("events", ctx.bpfEvents)
	if err != nil {
		return nil, err
	}
	ctx.bpfRingBuf.Start()

	ctx.bpfArgMap, err = ctx.bpfModule.GetMap("arg_map")
	if err != nil {
		return nil, err
	}

	// TODO(derekparker): This should eventually be moved to a more generalized place.
	go func() {
		for {
			b, ok := <-ctx.bpfEvents
			if !ok {
				return
			}

			parsed := parseFunctionParameterList(b)

			ctx.m.Lock()
			ctx.parsedBpfEvents = append(ctx.parsedBpfEvents, parsed)
			ctx.m.Unlock()
		}
	}()

	return &ctx, nil
}

func parseFunctionParameterList(rawParamBytes []byte) RawUProbeParams {
	params := (*C.function_parameter_list_t)(unsafe.Pointer(&rawParamBytes[0]))

	defer runtime.KeepAlive(params) // Ensure the param is not garbage collected.

	var rawParams RawUProbeParams
	rawParams.FnAddr = int(params.fn_addr)
	rawParams.GoroutineID = int(params.goroutine_id)

	parseParam := func(param C.function_parameter_t) *RawUProbeParam {
		iparam := &RawUProbeParam{}
		data := make([]byte, 0x60)
		ret := param
		iparam.Kind = reflect.Kind(ret.kind)

		val := C.GoBytes(unsafe.Pointer(&ret.val), C.int(ret.size))
		rawDerefValue := C.GoBytes(unsafe.Pointer(&ret.deref_val[0]), 0x30)
		copy(data, val)
		copy(data[0x30:], rawDerefValue)
		iparam.Data = data

		pieces := make([]op.Piece, 0, 2)
		pieces = append(pieces, op.Piece{Size: 0x30, Kind: op.AddrPiece, Val: FakeAddressBase})
		pieces = append(pieces, op.Piece{Size: 0x30, Kind: op.AddrPiece, Val: FakeAddressBase + 0x30})
		iparam.Pieces = pieces

		iparam.Addr = FakeAddressBase

		switch iparam.Kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			iparam.RealType = &godwarf.IntType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 8}}}
		case reflect.String:
			strLen := binary.LittleEndian.Uint64(val[8:])
			iparam.Base = FakeAddressBase + 0x30
			iparam.Len = int64(strLen)
		}
		return iparam
	}

	for i := 0; i < int(params.n_parameters); i++ {
		rawParams.InputParams = append(rawParams.InputParams, parseParam(params.params[i]))
	}
	for i := 0; i < int(params.n_ret_parameters); i++ {
		rawParams.ReturnParams = append(rawParams.ReturnParams, parseParam(params.ret_params[i]))
	}

	return rawParams
}

func createFunctionParameterList(entry uint64, goidOffset int64, args []UProbeArgMap, isret bool) C.function_parameter_list_t {
	var params C.function_parameter_list_t
	params.goid_offset = C.uint(goidOffset)
	params.fn_addr = C.uint(entry)
	params.is_ret = C.bool(isret)
	params.n_parameters = C.uint(0)
	params.n_ret_parameters = C.uint(0)
	for _, arg := range args {
		var param C.function_parameter_t
		param.size = C.uint(arg.Size)
		param.offset = C.int(arg.Offset)
		param.kind = C.uint(arg.Kind)
		if arg.InReg {
			param.in_reg = true
			param.n_pieces = C.int(len(arg.Pieces))
			for i := range arg.Pieces {
				if i > 5 {
					break
				}
				param.reg_nums[i] = C.int(arg.Pieces[i])
			}
		}
		if !arg.Ret {
			params.params[params.n_parameters] = param
			params.n_parameters++
		} else {
			params.ret_params[params.n_ret_parameters] = param
			params.n_ret_parameters++
		}
	}
	return params
}

func AddressToOffset(f *elf.File, addr uint64) (uint32, error) {
	sectionsToSearchForSymbol := []*elf.Section{}

	for i := range f.Sections {
		if f.Sections[i].Flags == elf.SHF_ALLOC+elf.SHF_EXECINSTR {
			sectionsToSearchForSymbol = append(sectionsToSearchForSymbol, f.Sections[i])
		}
	}

	var executableSection *elf.Section

	// Find what section the symbol is in by checking the executable section's
	// addr space.
	for m := range sectionsToSearchForSymbol {
		if addr > sectionsToSearchForSymbol[m].Addr &&
			addr < sectionsToSearchForSymbol[m].Addr+sectionsToSearchForSymbol[m].Size {
			executableSection = sectionsToSearchForSymbol[m]
		}
	}

	if executableSection == nil {
		return 0, errors.New("could not find symbol in executable sections of binary")
	}

	return uint32(addr - executableSection.Addr + executableSection.Offset), nil
}
