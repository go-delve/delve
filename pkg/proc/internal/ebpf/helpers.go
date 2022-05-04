//go:build linux && amd64 && cgo && go1.16
// +build linux,amd64,cgo,go1.16

package ebpf

// #include "./bpf/include/function_vals.bpf.h"
import "C"
import (
	"debug/elf"
	"encoding/binary"
	"errors"
	"reflect"
	"runtime"
	"sync"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags "go1.16" -target amd64 trace bpf/trace.bpf.c -- -I./bpf/include

const FakeAddressBase = 0xbeed000000000000

type EBPFContext struct {
	objs       *traceObjects
	bpfEvents  chan []byte
	bpfRingBuf *ringbuf.Reader
	executable *link.Executable
	bpfArgMap  *ebpf.Map

	parsedBpfEvents []RawUProbeParams
	m               sync.Mutex
}

func (ctx *EBPFContext) Close() {
	if ctx.objs != nil {
		ctx.objs.Close()
	}
}

func (ctx *EBPFContext) AttachUprobe(pid int, name string, offset uint64) error {
	if ctx.executable == nil {
		return errors.New("no eBPF program loaded")
	}
	_, err := ctx.executable.Uprobe(name, ctx.objs.tracePrograms.UprobeDlvTrace, &link.UprobeOptions{PID: pid, Offset: offset})
	return err
}

func (ctx *EBPFContext) UpdateArgMap(key uint64, goidOffset int64, args []UProbeArgMap, gAddrOffset uint64, isret bool) error {
	if ctx.bpfArgMap == nil {
		return errors.New("eBPF map not loaded")
	}
	params := createFunctionParameterList(key, goidOffset, args, isret)
	params.g_addr_offset = C.longlong(gAddrOffset)
	return ctx.bpfArgMap.Update(unsafe.Pointer(&key), unsafe.Pointer(&params), ebpf.UpdateAny)
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

func LoadEBPFTracingProgram(path string) (*EBPFContext, error) {
	var (
		ctx  EBPFContext
		err  error
		objs traceObjects
	)

	ctx.executable, err = link.OpenExecutable(path)
	if err != nil {
		return nil, err
	}

	if err := loadTraceObjects(&objs, nil); err != nil {
		return nil, err
	}
	ctx.objs = &objs

	ctx.bpfRingBuf, err = ringbuf.NewReader(objs.Events)
	if err != nil {
		return nil, err
	}

	ctx.bpfArgMap = objs.ArgMap

	// TODO(derekparker): This should eventually be moved to a more generalized place.
	go func() {
		for {
			e, err := ctx.bpfRingBuf.Read()
			if err != nil {
				return
			}

			parsed := parseFunctionParameterList(e.RawSample)

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

func AddressToOffset(f *elf.File, addr uint64) (uint64, error) {
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

	return uint64(addr - executableSection.Addr + executableSection.Offset), nil
}
