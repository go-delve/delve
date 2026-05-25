//go:build linux && amd64 && go1.16

package ebpf

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/logflags"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
)

//lint:file-ignore U1000 some fields are used by the C program

const (
	// fakeAddressUnresolv is the sentinel base address used for eBPF-captured
	// parameter values that live in a fake memory region. pkg/proc uses this
	// same value (via its own fakeAddressUnresolv constant) for composite
	// memory and CPU register variables; both must stay in sync.
	fakeAddressUnresolv = 0xbeed000000000000

	// Event type tags matching the BPF wire format.
	eventTypeHeader = 0
	eventTypeParam  = 1

	// maxValSize is the maximum byte size for val and deref_val buffers,
	// matching MAX_VAL_SIZE in trace.bpf.c.
	maxValSize = 0x30

	// Wire sizes of packed BPF ring buffer events.
	// Fields: type(1) + goroutine_id(8) + fn_addr(8) + is_ret(1) + n_params(4)
	eventHeaderWireSize = 1 + 8 + 8 + 1 + 4
	// Fields: type(1) + goroutine_id(8) + fn_addr(8) + param_idx(1) + is_ret(1) + kind(4) + val_size(4)
	paramEventWireSize = 1 + 8 + 8 + 1 + 1 + 4 + 4
	// paramEventWireSize + val + deref_val
	paramEventDataWireSize = paramEventWireSize + maxValSize + maxValSize

	maxPendingEvents = 1000
)

// function_parameter_t tracks function_parameter_t from function_vals.bpf.h
type function_parameter_t struct {
	kind      uint32
	size      uint32
	offset    int32
	in_reg    bool
	n_pieces  int32
	reg_nums  [6]int32
	daddr     uint64
	val       [maxValSize]byte
	deref_val [maxValSize]byte
}

// function_parameter_list_t tracks function_parameter_list_t from function_vals.bpf.h
type function_parameter_list_t struct {
	goid_offset   uint32
	_             [4]byte
	g_addr_offset uint64

	fn_addr uint64
	is_ret  bool
	_       [3]byte

	n_parameters uint32
	params       [6]function_parameter_t

	n_ret_parameters uint32
	ret_params       [6]function_parameter_t
}

// event_header_t mirrors the packed C event_header_t.
type event_header_t struct {
	Type         uint8
	Goroutine_id int64
	Fn_addr      uint64
	Is_ret       bool
	N_params     uint32
}

// param_event_t is the packed wire format for PARAM events from the BPF ring buffer.
type param_event_t struct {
	Type         uint8
	Goroutine_id int64
	Fn_addr      uint64
	Param_idx    uint8
	Is_ret       bool
	Kind         uint32
	Val_size     uint32
}

// dwarfTypeKey identifies a parameter for DWARF type lookup using a global
// index: input params are numbered 0..n-1, return params n..n+m-1.
type dwarfTypeKey struct {
	fnAddr   uint64
	paramIdx int
}

// paramMeta holds DWARF metadata for a traced parameter, populated during
// UpdateArgMap and looked up when parsing ring buffer PARAM events.
type paramMeta struct {
	dwarfType godwarf.Type
	name      string
}

// pendingKey associates PARAM events with their HEADER in the ring buffer
// protocol. Each uprobe hit produces one HEADER followed by N PARAM events;
// this key groups them so params can be collected until the set is complete.
type pendingKey struct {
	goroutineID int64
	fnAddr      uint64
	isRet       bool
}

type pendingEvent struct {
	header event_header_t
	params []*RawUProbeParam
}

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags "go1.16" -target amd64 trace bpf/trace.bpf.c -- -I./bpf/include

type EBPFContext struct {
	objs       *traceObjects
	bpfEvents  chan []byte
	bpfRingBuf *ringbuf.Reader
	executable *link.Executable
	bpfArgMap  *ebpf.Map
	links      []link.Link

	parsedBpfEvents []RawUProbeParams
	m               sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// paramInfo maps a global param index to its DWARF type and name.
	// Input params occupy indices 0..n-1; return params n..n+m-1.
	// Populated during UpdateArgMap, read during ring buffer event parsing.
	paramInfo    map[dwarfTypeKey]paramMeta
	nInputParams map[uint64]int // fnAddr → number of input parameters

	pendingEvents map[pendingKey]*pendingEvent
}

func (ctx *EBPFContext) Close() {
	if ctx.cancel != nil {
		ctx.cancel()
	}

	// Wait for pollEvents to finish draining. pollEvents uses a 10ms periodic
	// deadline so Read() unblocks naturally; no SetDeadline needed here.
	ctx.wg.Wait()

	ctx.m.Lock()
	for k, pe := range ctx.pendingEvents {
		ctx.emitParsedEvent(pe)
		delete(ctx.pendingEvents, k)
	}
	ctx.m.Unlock()

	if ctx.bpfRingBuf != nil {
		ctx.bpfRingBuf.Close()
	}

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

	// Store DWARF types and parameter names for later lookup during ring buffer
	// event parsing. Uses a global index: input params at 0..n-1, return params
	// at n..n+m-1. Held under ctx.m to prevent data race with pollEvents.
	ctx.m.Lock()
	if !isret {
		ctx.nInputParams[key] = len(args)
	}
	nInputs := ctx.nInputParams[key]
	for i, arg := range args {
		idx := i
		if isret {
			idx = nInputs + i
		}
		k := dwarfTypeKey{fnAddr: key, paramIdx: idx}
		ctx.paramInfo[k] = paramMeta{dwarfType: arg.DwarfType, name: arg.Name}
	}
	ctx.m.Unlock()

	params := createFunctionParameterList(key, goidOffset, args, isret)
	params.g_addr_offset = gAddrOffset
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
	ctx.paramInfo = make(map[dwarfTypeKey]paramMeta)
	ctx.nInputParams = make(map[uint64]int)
	ctx.pendingEvents = make(map[pendingKey]*pendingEvent)

	ctx.ctx, ctx.cancel = context.WithCancel(context.Background())

	ctx.wg.Add(1)
	go ctx.pollEvents()

	return &ctx, nil
}

// pollEvents reads events from the ring buffer and stores them for retrieval.
// This goroutine runs until the context is cancelled, then drains any events
// already buffered in the ring buffer before returning.
//
// A 10ms periodic deadline is set before each Read() call. This ensures
// Read() unblocks periodically to check for context cancellation, without
// requiring SetDeadline to be called from Close() while Read() holds r.mu
// (which would deadlock since SetDeadline also acquires r.mu).
func (ctx *EBPFContext) pollEvents() {
	defer ctx.wg.Done()

	for {
		if ctx.ctx.Err() != nil {
			ctx.drainRingBuf()
			return
		}

		// Set a short deadline before Read() so we unblock every 10ms to
		// check context cancellation. SetDeadline is safe here because Read()
		// is not yet running (both SetDeadline and Read() acquire r.mu).
		ctx.bpfRingBuf.SetDeadline(time.Now().Add(10 * time.Millisecond))
		e, err := ctx.bpfRingBuf.Read()
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				// Deadline fired — loop back and check context.
				continue
			}
			if ctx.ctx.Err() != nil {
				ctx.drainRingBuf()
			}
			return
		}

		ctx.storeEvent(e.RawSample)
	}
}

// drainRingBuf reads and stores all events currently buffered in the ring
// buffer. It sets a 50ms deadline so Read() returns quickly once the buffer
// is empty, rather than blocking indefinitely.
func (ctx *EBPFContext) drainRingBuf() {
	ctx.bpfRingBuf.SetDeadline(time.Now().Add(50 * time.Millisecond))
	for {
		e, err := ctx.bpfRingBuf.Read()
		if err != nil {
			return
		}
		ctx.storeEvent(e.RawSample)
	}
}

// storeEvent dispatches a raw ring buffer sample by event type.
func (ctx *EBPFContext) storeEvent(raw []byte) {
	if len(raw) < 1 {
		return
	}
	switch raw[0] {
	case eventTypeHeader:
		ctx.handleHeaderEvent(raw)
	case eventTypeParam:
		ctx.handleParamEvent(raw)
	}
}

func parseEventHeader(b []byte) (event_header_t, bool) {
	if len(b) < eventHeaderWireSize {
		return event_header_t{}, false
	}
	var h event_header_t
	h.Type = b[0]
	h.Goroutine_id = int64(binary.LittleEndian.Uint64(b[1:9]))
	h.Fn_addr = binary.LittleEndian.Uint64(b[9:17])
	h.Is_ret = b[17] != 0
	h.N_params = binary.LittleEndian.Uint32(b[18:22])
	return h, true
}

func parseParamEvent(b []byte) (param_event_t, bool) {
	if len(b) < paramEventWireSize {
		return param_event_t{}, false
	}
	var p param_event_t
	p.Type = b[0]
	p.Goroutine_id = int64(binary.LittleEndian.Uint64(b[1:9]))
	p.Fn_addr = binary.LittleEndian.Uint64(b[9:17])
	p.Param_idx = b[17]
	p.Is_ret = b[18] != 0
	p.Kind = binary.LittleEndian.Uint32(b[19:23])
	p.Val_size = binary.LittleEndian.Uint32(b[23:27])
	return p, true
}

func (ctx *EBPFContext) handleHeaderEvent(raw []byte) {
	hdr, ok := parseEventHeader(raw)
	if !ok {
		return
	}
	key := pendingKey{
		goroutineID: hdr.Goroutine_id,
		fnAddr:      hdr.Fn_addr,
		isRet:       hdr.Is_ret,
	}

	ctx.m.Lock()
	defer ctx.m.Unlock()
	if old, exists := ctx.pendingEvents[key]; exists {
		ctx.emitParsedEvent(old)
		delete(ctx.pendingEvents, key)
	}
	if len(ctx.pendingEvents) >= maxPendingEvents {
		for k, pe := range ctx.pendingEvents {
			ctx.emitParsedEvent(pe)
			delete(ctx.pendingEvents, k)
		}
	}
	if hdr.N_params == 0 {
		ctx.parsedBpfEvents = append(ctx.parsedBpfEvents, RawUProbeParams{
			FnAddr:      int(hdr.Fn_addr),
			GoroutineID: int(hdr.Goroutine_id),
			IsRet:       hdr.Is_ret,
		})
	} else {
		if hdr.N_params > 6 { // BPF-side MAX_PARAMS_PER_EVENT
			return
		}
		ctx.pendingEvents[key] = &pendingEvent{
			header: hdr,
			params: make([]*RawUProbeParam, hdr.N_params),
		}
	}
}

func (ctx *EBPFContext) handleParamEvent(raw []byte) {
	pe, ok := parseParamEvent(raw)
	if !ok {
		return
	}
	key := pendingKey{
		goroutineID: pe.Goroutine_id,
		fnAddr:      pe.Fn_addr,
		isRet:       pe.Is_ret,
	}

	ctx.m.Lock()
	defer ctx.m.Unlock()

	pending, exists := ctx.pendingEvents[key]
	if !exists {
		logflags.DebuggerLogger().Debugf("ebpf: dropped PARAM event for fn=%#x goroutine=%d is_ret=%v param_idx=%d: no matching HEADER (ring buffer loss?)", pe.Fn_addr, pe.Goroutine_id, pe.Is_ret, pe.Param_idx)
		return
	}

	if int(pe.Param_idx) >= len(pending.params) {
		logflags.DebuggerLogger().Debugf("ebpf: dropped PARAM event for fn=%#x goroutine=%d param_idx=%d: out of range (expected 0..%d)", pe.Fn_addr, pe.Goroutine_id, pe.Param_idx, len(pending.params)-1)
		return
	}

	iparam := &RawUProbeParam{}
	iparam.Kind = reflect.Kind(pe.Kind)
	iparam.Addr = fakeAddressUnresolv

	// Extract val and deref_val from fixed offsets after the param event header.
	valStart := paramEventWireSize
	derefValStart := paramEventWireSize + maxValSize

	data := make([]byte, 2*maxValSize)
	if valStart+maxValSize <= len(raw) {
		copy(data[:maxValSize], raw[valStart:valStart+maxValSize])
	}
	if derefValStart+maxValSize <= len(raw) {
		copy(data[maxValSize:], raw[derefValStart:derefValStart+maxValSize])
	}
	iparam.Data = data

	valSize := int(pe.Val_size)
	if valSize > maxValSize {
		valSize = maxValSize
	}

	iparam.Pieces = []op.Piece{
		{Size: valSize, Kind: op.AddrPiece, Val: fakeAddressUnresolv},
		{Size: maxValSize, Kind: op.AddrPiece, Val: fakeAddressUnresolv + uint64(valSize)},
	}

	// Map BPF's per-direction param_idx to the global DWARF type index.
	// Input params: 0..n-1, return params: n..n+m-1.
	globalIdx := int(pe.Param_idx)
	if pe.Is_ret {
		globalIdx = ctx.nInputParams[pe.Fn_addr] + int(pe.Param_idx)
	}
	dtKey := dwarfTypeKey{fnAddr: pe.Fn_addr, paramIdx: globalIdx}
	if meta, ok := ctx.paramInfo[dtKey]; ok {
		iparam.DwarfType = meta.dwarfType
		iparam.Name = meta.name
	}

	if iparam.DwarfType != nil {
		resolvedType := godwarf.ResolveTypedef(iparam.DwarfType)
		iparam.RealType = resolvedType
		switch resolvedType.(type) {
		case *godwarf.StructType:
			iparam.Kind = reflect.Struct
		case *godwarf.ArrayType:
			iparam.Kind = reflect.Array
		case *godwarf.SliceType:
			iparam.Kind = reflect.Slice
		default:
			if k := resolvedType.Common().ReflectKind; k != reflect.Invalid {
				iparam.Kind = k
			}
		}
		if iparam.Kind == reflect.String && len(iparam.Data) >= 16 {
			iparam.Base = fakeAddressUnresolv + uint64(valSize)
			iparam.Len = int64(binary.LittleEndian.Uint64(iparam.Data[8:16]))
		}
	} else {
		synthesizeTypeFromKind(iparam, pe.Val_size)
	}

	pending.params[pe.Param_idx] = iparam

	complete := true
	for _, p := range pending.params {
		if p == nil {
			complete = false
			break
		}
	}

	if complete {
		ctx.emitParsedEvent(pending)
		delete(ctx.pendingEvents, key)
	}
}

func (ctx *EBPFContext) emitParsedEvent(pe *pendingEvent) {
	result := RawUProbeParams{
		FnAddr:      int(pe.header.Fn_addr),
		GoroutineID: int(pe.header.Goroutine_id),
		IsRet:       pe.header.Is_ret,
	}
	for _, p := range pe.params {
		if p == nil {
			continue
		}
		if pe.header.Is_ret {
			result.ReturnParams = append(result.ReturnParams, p)
		} else {
			result.InputParams = append(result.InputParams, p)
		}
	}
	ctx.parsedBpfEvents = append(ctx.parsedBpfEvents, result)
}

// synthesizeTypeFromKind creates a godwarf.Type from a reflect.Kind
// for parameters that don't have a full DWARF type (backward
// compatibility with scalar types).
func synthesizeTypeFromKind(iparam *RawUProbeParam, valSize uint32) {
	vs := int64(valSize)
	switch iparam.Kind {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		iparam.RealType = &godwarf.UintType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: vs}}}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		iparam.RealType = &godwarf.IntType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: vs}}}
	case reflect.Bool:
		iparam.RealType = &godwarf.BoolType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 1, ReflectKind: reflect.Bool}}}
	case reflect.Float32, reflect.Float64:
		iparam.RealType = &godwarf.FloatType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: vs, ReflectKind: iparam.Kind}}}
	case reflect.Complex64, reflect.Complex128:
		iparam.RealType = &godwarf.ComplexType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: vs, ReflectKind: iparam.Kind}}}
	case reflect.Pointer, reflect.UnsafePointer:
		iparam.Kind = reflect.Uintptr
		iparam.RealType = &godwarf.UintType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: vs, ReflectKind: reflect.Uintptr}}}
	case reflect.Slice:
		iparam.Kind = reflect.Uintptr
		iparam.RealType = &godwarf.UintType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 8, ReflectKind: reflect.Uintptr}}}
	case reflect.String:
		if len(iparam.Data) >= 16 {
			iparam.Base = fakeAddressUnresolv + uint64(valSize)
			iparam.Len = int64(binary.LittleEndian.Uint64(iparam.Data[8:16]))
		}
		iparam.RealType = &godwarf.StringType{
			StructType: godwarf.StructType{
				CommonType: godwarf.CommonType{ByteSize: 16, ReflectKind: reflect.String},
				Kind:       "struct",
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
		iparam.Unreadable = fmt.Errorf("struct type not yet supported by ebpf tracing without DWARF type")
	case reflect.Array:
		iparam.Unreadable = fmt.Errorf("array type not yet supported by ebpf tracing without DWARF type")
	default:
		iparam.Unreadable = fmt.Errorf("unrecognized reflect.Kind %d from eBPF", iparam.Kind)
	}
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
		if addr >= sectionsToSearchForSymbol[m].Addr &&
			addr < sectionsToSearchForSymbol[m].Addr+sectionsToSearchForSymbol[m].Size {
			executableSection = sectionsToSearchForSymbol[m]
		}
	}

	if executableSection == nil {
		return 0, errors.New("could not find symbol in executable sections of binary")
	}

	return uint64(addr - executableSection.Addr + executableSection.Offset), nil
}
