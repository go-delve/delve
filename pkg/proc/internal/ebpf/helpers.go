//go:build linux && amd64 && go1.16

package ebpf

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
)

//lint:file-ignore U1000 some fields are used by the C program

// function_parameter_t tracks function_parameter_t from function_vals.bpf.h
type function_parameter_t struct {
	kind      uint32
	size      uint32
	offset    int32
	in_reg    bool
	n_pieces  int32
	reg_nums  [6]int32
	daddr     uint64
	val       [0x30]byte
	deref_val [0x30]byte
}

// function_parameter_list_t tracks function_parameter_list_t from function_vals.bpf.h
type function_parameter_list_t struct {
	goid_offset   uint32
	g_addr_offset uint64
	goroutine_id  uint32
	fn_addr       uint64
	is_ret        bool

	n_parameters uint32
	params       [6]function_parameter_t

	n_ret_parameters uint32
	ret_params       [6]function_parameter_t
}

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags "go1.16" -target amd64 trace bpf/trace.bpf.c -- -I./bpf/include

const FakeAddressBase = 0xbeed000000000000

type EBPFContext struct {
	objs       *traceObjects
	bpfEvents  chan []byte
	bpfRingBuf *ringbuf.Reader
	executable *link.Executable
	bpfArgMap  *ebpf.Map
	links      []link.Link

	parsedBpfEvents []RawUProbeParams
	argTypeInfo     map[uint64][]UProbeArgMap // Maps function address to argument type information
	m               sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (ctx *EBPFContext) Close() {
	if ctx.cancel != nil {
		ctx.cancel()
	}

	if ctx.bpfRingBuf != nil {
		ctx.bpfRingBuf.Close()
	}

	ctx.wg.Wait()

	for _, l := range ctx.links {
		l.Close()
	}

	if ctx.objs != nil {
		ctx.objs.Close()
	}
}

func (ctx *EBPFContext) AttachUprobe(pid int, name string, offset uint64) error {
	if ctx.executable == nil {
		return errors.New("no eBPF program loaded")
	}
	l, err := ctx.executable.Uprobe(name, ctx.objs.tracePrograms.UprobeDlvTrace, &link.UprobeOptions{PID: pid, Address: offset})
	ctx.links = append(ctx.links, l)
	return err
}

func (ctx *EBPFContext) UpdateArgMap(key uint64, goidOffset int64, args []UProbeArgMap, gAddrOffset uint64, isret bool) error {
	if ctx.bpfArgMap == nil {
		return errors.New("eBPF map not loaded")
	}
	params := createFunctionParameterList(key, goidOffset, args, isret)
	params.g_addr_offset = gAddrOffset

	// Store argument type information for later use when parsing results
	ctx.m.Lock()
	if ctx.argTypeInfo == nil {
		ctx.argTypeInfo = make(map[uint64][]UProbeArgMap)
	}
	ctx.argTypeInfo[key] = args
	ctx.m.Unlock()

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

	if err = rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("failed to remove memlock limit (try running with CAP_SYS_RESOURCE or as root): %w", err)
	}
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

	ctx.ctx, ctx.cancel = context.WithCancel(context.Background())

	ctx.wg.Add(1)
	go ctx.pollEvents()

	return &ctx, nil
}

// pollEvents reads events from the ring buffer and stores them for retrieval.
// This goroutine runs until the context is cancelled.
func (ctx *EBPFContext) pollEvents() {
	defer ctx.wg.Done()

	for {
		select {
		case <-ctx.ctx.Done():
			return
		default:
			e, err := ctx.bpfRingBuf.Read()
			if err != nil || ctx.ctx.Err() != nil {
				return
			}

			parsed := parseFunctionParameterList(ctx, e.RawSample)

			ctx.m.Lock()
			ctx.parsedBpfEvents = append(ctx.parsedBpfEvents, parsed)
			ctx.m.Unlock()
		}
	}
}

func parseFunctionParameterList(ctx *EBPFContext, rawParamBytes []byte) RawUProbeParams {
	params := (*function_parameter_list_t)(unsafe.Pointer(&rawParamBytes[0]))

	defer runtime.KeepAlive(params) // Ensure the param is not garbage collected.

	var rawParams RawUProbeParams
	rawParams.FnAddr = int(params.fn_addr)
	rawParams.GoroutineID = int(params.goroutine_id)
	rawParams.IsRet = params.is_ret

	// Look up original type information for this function
	ctx.m.Lock()
	argTypes := ctx.argTypeInfo[params.fn_addr]
	ctx.m.Unlock()

	parseParam := func(param function_parameter_t, paramIdx int) *RawUProbeParam {
		iparam := &RawUProbeParam{}
		data := make([]byte, 0x60)
		ret := param
		iparam.Kind = reflect.Kind(ret.kind)

		// Populate name and type name from argTypes if available
		if paramIdx < len(argTypes) {
			iparam.Name = argTypes[paramIdx].Name
			iparam.TypeName = argTypes[paramIdx].TypeName
		}

		val := ret.val[:ret.size]
		rawDerefValue := ret.deref_val[:0x30]
		copy(data, val)
		copy(data[0x30:], rawDerefValue)
		iparam.Data = data

		pieces := make([]op.Piece, 0, 2)
		pieces = append(pieces, op.Piece{Size: 0x30, Kind: op.AddrPiece, Val: FakeAddressBase})
		pieces = append(pieces, op.Piece{Size: 0x30, Kind: op.AddrPiece, Val: FakeAddressBase + 0x30})
		iparam.Pieces = pieces

		iparam.Addr = FakeAddressBase

		switch iparam.Kind {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			typeName := iparam.Kind.String()
			iparam.RealType = &godwarf.UintType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: int64(ret.size), Name: typeName, ReflectKind: iparam.Kind}}}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			typeName := iparam.Kind.String()
			iparam.RealType = &godwarf.IntType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: int64(ret.size), Name: typeName, ReflectKind: iparam.Kind}}}
		case reflect.Bool:
			iparam.RealType = &godwarf.BoolType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 1, Name: "bool", ReflectKind: reflect.Bool}}}
		case reflect.Float32, reflect.Float64:
			if !usesXMMRegisters(ret) {
				typeName := iparam.Kind.String()
				iparam.RealType = &godwarf.FloatType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: int64(ret.size), Name: typeName, ReflectKind: iparam.Kind}}}
			}
			// If in XMM registers, RealType stays nil, marked unreadable in target.go
		case reflect.Complex64, reflect.Complex128:
			if !usesXMMRegisters(ret) {
				typeName := iparam.Kind.String()
				iparam.RealType = &godwarf.ComplexType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: int64(ret.size), Name: typeName, ReflectKind: iparam.Kind}}}
			}
		case reflect.Ptr, reflect.UnsafePointer:
			// Display the raw pointer address as a uintptr value.
			// The eBPF probe captures dereferenced data into deref_val
			// (up to 0x30 bytes), but we can't use it here because:
			// 1) loadPtr needs the element type, which isn't propagated
			//    through the eBPF pipeline (only Kind and Size are sent)
			// 2) deref_val is limited to 48 bytes, insufficient for
			//    nested or variable-length pointed-to types
			iparam.Kind = reflect.Uintptr
			iparam.RealType = &godwarf.UintType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: int64(ret.size), Name: "uintptr", ReflectKind: reflect.Uintptr}}}
		case reflect.Slice:
			// Display the slice data pointer address as a uintptr value.
			// Same limitations as pointers above: element type info is
			// not available, and deref_val (48 bytes) can only hold a
			// few elements.
			iparam.Kind = reflect.Uintptr
			iparam.RealType = &godwarf.UintType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 8, Name: "uintptr", ReflectKind: reflect.Uintptr}}}
		case reflect.String:
			strLen := binary.LittleEndian.Uint64(val[8:])
			iparam.Base = FakeAddressBase + 0x30
			iparam.Len = int64(strLen)
			iparam.RealType = &godwarf.StringType{
				StructType: godwarf.StructType{
					CommonType: godwarf.CommonType{
						ByteSize:    16,
						Name:        "string",
						ReflectKind: reflect.String,
					},
					Kind: "struct",
				},
			}
		case reflect.Map:
			iparam.Unreadable = fmt.Errorf("map type not yet supported by ebpf tracing")
		case reflect.Chan:
			iparam.Unreadable = fmt.Errorf("chan type not yet supported by ebpf tracing")
		case reflect.Interface:
			iparam.Unreadable = fmt.Errorf("interface type not yet supported by ebpf tracing")
		case reflect.Func:
			iparam.Unreadable = fmt.Errorf("func type not yet supported by ebpf tracing")
		case reflect.Struct:
			iparam.Unreadable = fmt.Errorf("struct type not yet supported by ebpf tracing")
		case reflect.Array:
			iparam.Unreadable = fmt.Errorf("array type not yet supported by ebpf tracing")
		}
		return iparam
	}

	for i := 0; i < int(params.n_parameters); i++ {
		rawParams.InputParams = append(rawParams.InputParams, parseParam(params.params[i], i))
	}
	// Return parameters start after input parameters in argTypes
	for i := 0; i < int(params.n_ret_parameters); i++ {
		rawParams.ReturnParams = append(rawParams.ReturnParams, parseParam(params.ret_params[i], int(params.n_parameters)+i))
	}

	return rawParams
}

// usesXMMRegisters returns true if the parameter is passed in XMM/SSE
// registers, which are not accessible from eBPF uprobes.
func usesXMMRegisters(param function_parameter_t) bool {
	if !param.in_reg {
		return false
	}
	for i := 0; i < int(param.n_pieces); i++ {
		if param.reg_nums[i] >= regnum.AMD64_XMM0 {
			return true
		}
	}
	return false
}

func createFunctionParameterList(entry uint64, goidOffset int64, args []UProbeArgMap, isret bool) function_parameter_list_t {
	var params function_parameter_list_t
	params.goid_offset = uint32(goidOffset)
	params.fn_addr = entry
	params.is_ret = isret
	params.n_parameters = 0
	params.n_ret_parameters = 0
	for _, arg := range args {
		var param function_parameter_t
		param.size = uint32(arg.Size)
		param.offset = int32(arg.Offset)
		param.kind = uint32(arg.Kind)
		if arg.InReg {
			param.in_reg = true
			param.n_pieces = int32(len(arg.Pieces))
			for i := range arg.Pieces {
				if i > 5 {
					break
				}
				param.reg_nums[i] = int32(arg.Pieces[i])
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
