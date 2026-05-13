//go:build linux && amd64 && cgo && go1.16

package ebpf

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc/internal/ebpf/testhelper"
)

func TestBuildDerefPlanStruct(t *testing.T) {
	// Test struct with a pointer field and a string field.
	structType := &godwarf.StructType{
		CommonType: godwarf.CommonType{
			ByteSize:    32, // int(8) + *int(8) + string(16)
			ReflectKind: reflect.Struct,
		},
		Kind:       "struct",
		StructName: "TestStruct",
		Field: []*godwarf.StructField{
			{
				Name:       "A",
				ByteOffset: 0,
				Type: &godwarf.IntType{
					BasicType: godwarf.BasicType{
						CommonType: godwarf.CommonType{ByteSize: 8},
					},
				},
			},
			{
				Name:       "B",
				ByteOffset: 8,
				Type: &godwarf.PtrType{
					CommonType: godwarf.CommonType{ByteSize: 8},
					Type: &godwarf.IntType{
						BasicType: godwarf.BasicType{
							CommonType: godwarf.CommonType{ByteSize: 8},
						},
					},
				},
			},
			{
				Name:       "C",
				ByteOffset: 16,
				Type: &godwarf.StringType{
					StructType: godwarf.StructType{
						CommonType: godwarf.CommonType{ByteSize: 16, ReflectKind: reflect.String},
						Kind:       "struct",
					},
				},
			},
		},
	}

	derefs, numDerefs := BuildDerefPlan(structType)
	if numDerefs != 2 {
		t.Fatalf("expected 2 derefs, got %d", numDerefs)
	}
	if derefs[0].Offset != 8 {
		t.Errorf("deref[0] offset: expected 8, got %d", derefs[0].Offset)
	}
	if derefs[0].Size != 8 {
		t.Errorf("deref[0] size: expected 8, got %d", derefs[0].Size)
	}
	if derefs[1].Offset != 16 {
		t.Errorf("deref[1] offset: expected 16, got %d", derefs[1].Offset)
	}
	if derefs[1].Size == 0 {
		t.Error("deref[1] size should be non-zero")
	}
}

func TestBuildDerefPlanScalarArray(t *testing.T) {
	arrayType := &godwarf.ArrayType{
		CommonType: godwarf.CommonType{
			ByteSize:    32,
			ReflectKind: reflect.Array,
		},
		Type: &godwarf.IntType{
			BasicType: godwarf.BasicType{
				CommonType: godwarf.CommonType{ByteSize: 8},
			},
		},
		Count: 4,
	}

	_, numDerefs := BuildDerefPlan(arrayType)
	if numDerefs != 0 {
		t.Errorf("expected 0 derefs for scalar array, got %d", numDerefs)
	}
}

func TestBuildDerefPlanPointer(t *testing.T) {
	ptrType := &godwarf.PtrType{
		CommonType: godwarf.CommonType{ByteSize: 8},
		Type: &godwarf.IntType{
			BasicType: godwarf.BasicType{
				CommonType: godwarf.CommonType{ByteSize: 8},
			},
		},
	}

	derefs, numDerefs := BuildDerefPlan(ptrType)
	if numDerefs != 1 {
		t.Fatalf("expected 1 deref, got %d", numDerefs)
	}
	if derefs[0].Offset != 0 {
		t.Errorf("deref[0] offset: expected 0, got %d", derefs[0].Offset)
	}
	if derefs[0].Size != 8 {
		t.Errorf("deref[0] size: expected 8, got %d", derefs[0].Size)
	}
}

func compareStructTypes(t *testing.T, gostructVal, cstructVal any) {
	t.Helper()
	gostruct := reflect.ValueOf(gostructVal).Type()
	cstruct := reflect.ValueOf(cstructVal).Type()

	if gostruct.Size() != cstruct.Size() {
		t.Errorf("total struct size mismatch: Go=%d C=%d", gostruct.Size(), cstruct.Size())
	}

	goFields := make([]reflect.StructField, 0, gostruct.NumField())
	for i := 0; i < gostruct.NumField(); i++ {
		f := gostruct.Field(i)
		if f.Name != "_" {
			goFields = append(goFields, f)
		}
	}

	cFields := make([]reflect.StructField, 0, cstruct.NumField())
	for i := 0; i < cstruct.NumField(); i++ {
		f := cstruct.Field(i)
		if f.Name != "_" {
			cFields = append(cFields, f)
		}
	}

	if len(goFields) != len(cFields) {
		t.Errorf("mismatched non-padding field number: Go=%d C=%d", len(goFields), len(cFields))
		return
	}
	for i := 0; i < len(cFields); i++ {
		gofield := goFields[i]
		cfield := cFields[i]
		t.Logf("%d %s %s\n", i, gofield.Name, cfield.Name)
		if gofield.Name != cfield.Name {
			t.Errorf("mismatched name for field %s %s", gofield.Name, cfield.Name)
		}
		if gofield.Offset != cfield.Offset {
			t.Errorf("mismatched offset for field %s %s (%d %d)", gofield.Name, cfield.Name, gofield.Offset, cfield.Offset)
		}
		if gofield.Type.Size() != cfield.Type.Size() {
			t.Errorf("mismatched size for field %s %s (%d %d)", gofield.Name, cfield.Name, gofield.Type.Size(), cfield.Type.Size())
		}
	}
}

func TestEventReassembly(t *testing.T) {
	ctx := &EBPFContext{
		pendingEvents: make(map[pendingKey]*pendingEvent),
		dwarfTypes:    make(map[dwarfTypeKey]godwarf.Type),
	}

	hdrBuf := make([]byte, eventHeaderWireSize)
	hdrBuf[0] = eventTypeHeader
	binary.LittleEndian.PutUint64(hdrBuf[1:9], uint64(42)) // goroutine_id
	binary.LittleEndian.PutUint64(hdrBuf[9:17], 0x1000)    // fn_addr
	hdrBuf[17] = 0                                          // is_ret = false
	binary.LittleEndian.PutUint32(hdrBuf[18:22], 1)         // n_params

	// Build PARAM event bytes (63-byte header + 4 bytes val data).
	valData := make([]byte, 4)
	binary.LittleEndian.PutUint32(valData, 99)

	paramBuf := make([]byte, paramEventWireSize+4)
	paramBuf[0] = eventTypeParam
	binary.LittleEndian.PutUint64(paramBuf[1:9], uint64(42))              // goroutine_id
	binary.LittleEndian.PutUint64(paramBuf[9:17], 0x1000)                 // fn_addr
	paramBuf[17] = 0                                                       // param_idx
	paramBuf[18] = 0                                                       // is_ret = false
	binary.LittleEndian.PutUint32(paramBuf[19:23], uint32(reflect.Int32)) // kind
	binary.LittleEndian.PutUint32(paramBuf[23:27], 4)                     // val_size
	binary.LittleEndian.PutUint32(paramBuf[27:31], 0)                     // n_derefs
	// deref_sizes[0..7] are already zero from make()
	copy(paramBuf[paramEventWireSize:], valData)

	// Feed events into the context.
	ctx.storeEvent(hdrBuf)
	ctx.storeEvent(paramBuf)

	// Verify reassembled result.
	ctx.m.Lock()
	defer ctx.m.Unlock()

	if len(ctx.parsedBpfEvents) != 1 {
		t.Fatalf("expected 1 parsed event, got %d", len(ctx.parsedBpfEvents))
	}
	result := ctx.parsedBpfEvents[0]
	if result.GoroutineID != 42 {
		t.Errorf("expected goroutine_id 42, got %d", result.GoroutineID)
	}
	if result.FnAddr != 0x1000 {
		t.Errorf("expected fn_addr 0x1000, got %d", result.FnAddr)
	}
	if len(result.InputParams) != 1 {
		t.Fatalf("expected 1 input param, got %d", len(result.InputParams))
	}
	p := result.InputParams[0]
	if p.Kind != reflect.Int32 {
		t.Errorf("expected kind Int32, got %v", p.Kind)
	}
	if len(p.Data) < 4 {
		t.Fatalf("expected data len >= 4, got %d", len(p.Data))
	}
	val := binary.LittleEndian.Uint32(p.Data[:4])
	if val != 99 {
		t.Errorf("expected data value 99, got %d", val)
	}
	if p.Addr != FakeAddressBase {
		t.Errorf("expected Addr FakeAddressBase, got 0x%x", p.Addr)
	}
	if len(p.Pieces) != 1 {
		t.Fatalf("expected 1 piece, got %d", len(p.Pieces))
	}
	if p.Pieces[0].Size != 4 {
		t.Errorf("expected piece size 4, got %d", p.Pieces[0].Size)
	}
	if p.Pieces[0].Val != FakeAddressBase {
		t.Errorf("expected piece Val FakeAddressBase, got 0x%x", p.Pieces[0].Val)
	}
	if p.Pieces[0].Kind != op.AddrPiece {
		t.Errorf("expected piece Kind AddrPiece, got %v", p.Pieces[0].Kind)
	}
	if p.RealType == nil {
		t.Fatal("expected non-nil RealType")
	}
	intType, ok := p.RealType.(*godwarf.IntType)
	if !ok {
		t.Fatalf("expected *godwarf.IntType, got %T", p.RealType)
	}
	if intType.ByteSize != 4 {
		t.Errorf("expected IntType ByteSize 4, got %d", intType.ByteSize)
	}
}

func TestEventReassemblyPartial(t *testing.T) {
	ctx := &EBPFContext{
		pendingEvents: make(map[pendingKey]*pendingEvent),
		dwarfTypes:    make(map[dwarfTypeKey]godwarf.Type),
	}

	hdrBuf := make([]byte, eventHeaderWireSize)
	hdrBuf[0] = eventTypeHeader
	binary.LittleEndian.PutUint64(hdrBuf[1:9], uint64(1))
	binary.LittleEndian.PutUint64(hdrBuf[9:17], 0x2000)
	hdrBuf[17] = 0
	binary.LittleEndian.PutUint32(hdrBuf[18:22], 2) // n_params = 2

	ctx.storeEvent(hdrBuf)

	// Only send param_idx=0, skip param_idx=1.
	paramBuf := make([]byte, paramEventWireSize+4)
	paramBuf[0] = eventTypeParam
	binary.LittleEndian.PutUint64(paramBuf[1:9], uint64(1))
	binary.LittleEndian.PutUint64(paramBuf[9:17], 0x2000)
	paramBuf[17] = 0 // param_idx=0
	paramBuf[18] = 0
	binary.LittleEndian.PutUint32(paramBuf[19:23], uint32(reflect.Int))
	binary.LittleEndian.PutUint32(paramBuf[23:27], 4)
	binary.LittleEndian.PutUint32(paramBuf[27:31], 0)
	binary.LittleEndian.PutUint32(paramBuf[paramEventWireSize:], 42)

	ctx.storeEvent(paramBuf)

	// Now send a NEW header for same key -- forces flush of incomplete event.
	ctx.storeEvent(hdrBuf)

	ctx.m.Lock()
	defer ctx.m.Unlock()

	if len(ctx.parsedBpfEvents) != 1 {
		t.Fatalf("expected 1 flushed event, got %d", len(ctx.parsedBpfEvents))
	}
	if len(ctx.parsedBpfEvents[0].InputParams) != 1 {
		t.Errorf("expected 1 partial param, got %d", len(ctx.parsedBpfEvents[0].InputParams))
	}
}

func TestPackedWireSizes(t *testing.T) {
	t.Run("event_header", func(t *testing.T) {
		buf := make([]byte, eventHeaderWireSize)
		buf[0] = eventTypeHeader
		binary.LittleEndian.PutUint64(buf[1:9], 0x0102030405060708)
		binary.LittleEndian.PutUint64(buf[9:17], 0xAAAABBBBCCCCDDDD)
		buf[17] = 1
		binary.LittleEndian.PutUint32(buf[18:22], 5)

		h, ok := parseEventHeader(buf)
		if !ok {
			t.Fatal("parseEventHeader failed")
		}
		if h.Goroutine_id != 0x0102030405060708 {
			t.Errorf("Goroutine_id: want 0x0102030405060708, got 0x%x", h.Goroutine_id)
		}
		if h.Fn_addr != 0xAAAABBBBCCCCDDDD {
			t.Errorf("Fn_addr: want 0xAAAABBBBCCCCDDDD, got 0x%x", h.Fn_addr)
		}
		if !h.Is_ret {
			t.Error("Is_ret: want true, got false")
		}
		if h.N_params != 5 {
			t.Errorf("N_params: want 5, got %d", h.N_params)
		}

		// Verify a short buffer is rejected.
		if _, ok := parseEventHeader(buf[:eventHeaderWireSize-1]); ok {
			t.Error("parseEventHeader should reject short buffer")
		}
	})

	t.Run("param_event", func(t *testing.T) {
		buf := make([]byte, paramEventWireSize)
		buf[0] = eventTypeParam
		binary.LittleEndian.PutUint64(buf[1:9], 999)
		binary.LittleEndian.PutUint64(buf[9:17], 0x4000)
		buf[17] = 3
		buf[18] = 1
		binary.LittleEndian.PutUint32(buf[19:23], uint32(reflect.Int64))
		binary.LittleEndian.PutUint32(buf[23:27], 8)
		binary.LittleEndian.PutUint32(buf[27:31], 2)
		binary.LittleEndian.PutUint32(buf[31:35], 100)
		binary.LittleEndian.PutUint32(buf[35:39], 200)

		p, ok := parseParamEvent(buf)
		if !ok {
			t.Fatal("parseParamEvent failed")
		}
		if p.Goroutine_id != 999 {
			t.Errorf("Goroutine_id: want 999, got %d", p.Goroutine_id)
		}
		if p.Fn_addr != 0x4000 {
			t.Errorf("Fn_addr: want 0x4000, got 0x%x", p.Fn_addr)
		}
		if p.Param_idx != 3 {
			t.Errorf("Param_idx: want 3, got %d", p.Param_idx)
		}
		if !p.Is_ret {
			t.Error("Is_ret: want true, got false")
		}
		if p.Kind != uint32(reflect.Int64) {
			t.Errorf("Kind: want %d, got %d", reflect.Int64, p.Kind)
		}
		if p.Val_size != 8 {
			t.Errorf("Val_size: want 8, got %d", p.Val_size)
		}
		if p.N_derefs != 2 {
			t.Errorf("N_derefs: want 2, got %d", p.N_derefs)
		}
		if p.Deref_sizes[0] != 100 || p.Deref_sizes[1] != 200 {
			t.Errorf("Deref_sizes: want [100,200,...], got %v", p.Deref_sizes)
		}

		if _, ok := parseParamEvent(buf[:paramEventWireSize-1]); ok {
			t.Error("parseParamEvent should reject short buffer")
		}
	})
}

func TestStructConsistency(t *testing.T) {
	t.Run("deref_entry_t", func(t *testing.T) {
		compareStructTypes(t, deref_entry_t{}, testhelper.Deref_entry_t{})
	})
	t.Run("function_parameter_t", func(t *testing.T) {
		compareStructTypes(t, function_parameter_t{}, testhelper.Function_parameter_t{})
	})
	t.Run("function_parameter_list_t", func(t *testing.T) {
		compareStructTypes(t, function_parameter_list_t{}, testhelper.Function_parameter_list_t{})
	})
}
