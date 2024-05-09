package proc

import (
	"errors"
	"fmt"
	"go/constant"
)

const heapInfoSize = 512

// Information for heapInfoSize bytes of heap.
type heapInfo struct {
	base     Address // start of the span containing this heap region
	size     int64   // size of objects in the span
	mark     uint64  // 64 mark bits, one for every 8 bytes
	firstIdx int     // the index of the first object that starts in this region, or -1 if none
	// For 64-bit inferiors, ptr[0] contains 64 pointer bits, one
	// for every 8 bytes.  On 32-bit inferiors, ptr contains 128
	// pointer bits, one for every 4 bytes.
	ptr [2]uint64
}

func (h *heapInfo) IsPtr(a Address, ptrSize int64) bool {
	if ptrSize == 8 {
		i := uint(a%heapInfoSize) / 8
		return h.ptr[0]>>i&1 != 0
	}
	i := a % heapInfoSize / 4
	return h.ptr[i/64]>>(i%64)&1 != 0
}

type HeapScope struct {
	// data structure for fast object finding
	// The key to these maps is the object address divided by
	// pageTableSize * heapInfoSize.
	pageTable map[Address]*pageTableEntry

	specials []*Variable

	mds []moduleData

	mem   MemoryReadWriter
	bi    *BinaryInfo
	scope *EvalScope
}

// setHeapPtr records that the memory at heap address a contains a pointer.
func (s *HeapScope) setHeapPtr(a Address) {
	h := s.allocHeapInfo(a)
	if s.bi.Arch.PtrSize() == 8 {
		i := uint(a%heapInfoSize) / 8
		h.ptr[0] |= uint64(1) << i
		return
	}
	i := a % heapInfoSize / 4
	h.ptr[i/64] |= uint64(1) << (i % 64)
}

// Heap info structures cover 9 bits of address.
// A page table entry covers 20 bits of address (1MB).
const pageTableSize = 1 << 11

type pageTableEntry [pageTableSize]heapInfo

// findHeapInfo finds the heapInfo structure for a.
// Returns nil if a is not a heap address.
func (s *HeapScope) findHeapInfo(a Address) *heapInfo {
	k := a / heapInfoSize / pageTableSize
	i := a / heapInfoSize % pageTableSize
	pt := s.pageTable[k]
	if pt == nil {
		return nil
	}
	h := &pt[i]
	if h.base == 0 {
		return nil
	}
	return h
}

// Same as findHeapInfo, but allocates the heapInfo if it
// hasn't been allocated yet.
func (s *HeapScope) allocHeapInfo(a Address) *heapInfo {
	k := a / heapInfoSize / pageTableSize
	i := a / heapInfoSize % pageTableSize
	pt := s.pageTable[k]
	if pt == nil {
		pt = new(pageTableEntry)
		for j := 0; j < pageTableSize; j++ {
			pt[j].firstIdx = -1
		}
		s.pageTable[k] = pt
	}
	return &pt[i]
}

// Size returns the size of x in bytes.
func (s *HeapScope) Size(x Address) int64 {
	return s.findHeapInfo(x).size
}

// isPtrFromHeap reports whether the inferior at address a contains a pointer.
// a must be somewhere in the heap.
func (s *HeapScope) isPtrFromHeap(a Address) bool {
	h := s.findHeapInfo(a)
	return h != nil && h.IsPtr(a, int64(s.bi.Arch.PtrSize()))
}

// arena is a summary of the size of components of a heapArena.
type arena struct {
	heapMin Address
	heapMax Address

	// Optional.
	bitmapMin Address
	bitmapMax Address

	spanTableMin Address
	spanTableMax Address
}

func (s *HeapScope) readHeap() error {
	ptrSize := s.bi.Arch.PtrSize()
	rdr := s.bi.Images[0].DwarfReader()
	if rdr == nil {
		return errors.New("error dwarf reader is nil")
	}

	s.pageTable = map[Address]*pageTableEntry{}

	tmp, err := s.scope.findGlobal("runtime", "mheap_")
	if err != nil {
		return err
	}

	mheap := tmp.toRegion()

	var arenas []arena

	if mheap.HasField("spans") {
		// go 1.9 or 1.10. There is a single arena.
		arenas = append(arenas, s.readArena19(mheap))
	} else {
		// go 1.11+. Has multiple arenas.
		arenaSize := s.rtConstant("heapArenaBytes")
		if arenaSize%heapInfoSize != 0 {
			return errors.New("arenaSize not a multiple of heapInfoSize")
		}
		arenaBaseOffset := s.getArenaBaseOffset()
		if ptrSize == 4 && arenaBaseOffset != 0 {
			return errors.New("arenaBaseOffset must be 0 for 32-bit inferior")
		}

		level1Table := mheap.Field("arenas")
		level1size := level1Table.ArrayLen()
		for level1 := int64(0); level1 < level1size; level1++ {
			ptr := level1Table.ArrayIndex(level1)
			if ptr.Address() == 0 {
				continue
			}
			level2table := ptr.Deref()
			level2size := level2table.ArrayLen()
			for level2 := int64(0); level2 < level2size; level2++ {
				ptr = level2table.ArrayIndex(level2)
				if ptr.Address() == 0 {
					continue
				}
				a := ptr.Deref()

				min := Address(arenaSize*(level2+level1*level2size) - arenaBaseOffset)
				max := min.Add(arenaSize)

				arenas = append(arenas, s.readArena(a, min, max))
			}
		}
	}

	return s.readSpans(mheap, arenas)
}

func (s *HeapScope) getArenaBaseOffset() int64 {
	if x, err := s.scope.findGlobal("runtime", "arenaBaseOffsetUintptr"); err == nil { // go1.15+
		// arenaBaseOffset changed sign in 1.15. Callers treat this
		// value as it was specified in 1.14, so we negate it here.
		xv, _ := constant.Int64Val(x.Value)
		return -xv
	}
	x, _ := s.scope.findGlobal("runtime", "arenaBaseOffset")
	xv, _ := constant.Int64Val(x.Value)
	return xv
}

// Read the global arena. Go 1.9 or 1.10 only, which has a single arena. Record
// heap pointers and return the arena size summary.
func (s *HeapScope) readArena19(mheap *region) arena {
	ptrSize := int64(s.bi.Arch.PtrSize())
	logPtrSize := s.bi.Arch.LogPtrSize()

	arenaStart := Address(mheap.Field("arena_start").Uintptr())
	arenaUsed := Address(mheap.Field("arena_used").Uintptr())
	arenaEnd := Address(mheap.Field("arena_end").Uintptr())
	bitmapEnd := Address(mheap.Field("bitmap").Uintptr())
	bitmapStart := bitmapEnd.Add(-int64(mheap.Field("bitmap_mapped").Uintptr()))
	spanTableStart := mheap.Field("spans").SliceIndex(0).a
	spanTableEnd := spanTableStart.Add(mheap.Field("spans").SliceCap() * ptrSize)

	// Copy pointer bits to heap info.
	// Note that the pointer bits are stored backwards.
	for a := arenaStart; a < arenaUsed; a = a.Add(ptrSize) {
		off := a.Sub(arenaStart) >> logPtrSize

		bme, _ := readUintRaw(s.mem, uint64(bitmapEnd.Add(-(off>>2)-1)), 1)
		if uint8(bme)>>uint(off&3)&1 != 0 {
			s.setHeapPtr(a)
		}
	}

	return arena{
		heapMin:      arenaStart,
		heapMax:      arenaEnd,
		bitmapMin:    bitmapStart,
		bitmapMax:    bitmapEnd,
		spanTableMin: spanTableStart,
		spanTableMax: spanTableEnd,
	}
}

// Read a single heapArena. Go 1.11+, which has multiple areans. Record heap
// pointers and return the arena size summary.
func (s *HeapScope) readArena(a *region, min, max Address) arena {
	ptrSize := s.bi.Arch.PtrSize()

	var bitmap *region
	if a.HasField("bitmap") { // Before go 1.22
		bitmap = a.Field("bitmap")
		if oneBitBitmap := a.HasField("noMorePtrs"); oneBitBitmap { // Starting in go 1.20
			s.readOneBitBitmap(bitmap, min)
		} else {
			s.readMultiBitBitmap(bitmap, min)
		}
	} else if a.HasField("heapArenaPtrScalar") && a.Field("heapArenaPtrScalar").HasField("bitmap") { // go 1.22 without allocation headers
		// TODO: This configuration only existed between CL 537978 and CL
		// 538217 and was never released. Prune support.
		bitmap = a.Field("heapArenaPtrScalar").Field("bitmap")
		s.readOneBitBitmap(bitmap, min)
	} else { // go 1.22 with allocation headers
		panic("unimplemented")
	}

	spans := a.Field("spans")
	arena := arena{
		heapMin:      min,
		heapMax:      max,
		spanTableMin: spans.a,
		spanTableMax: spans.a.Add(spans.ArrayLen() * int64(ptrSize)),
	}
	if bitmap.a != 0 {
		arena.bitmapMin = bitmap.a
		arena.bitmapMax = bitmap.a.Add(bitmap.ArrayLen())
	}
	return arena
}

// Read a one-bit bitmap (Go 1.20+), recording the heap pointers.
func (s *HeapScope) readOneBitBitmap(bitmap *region, min Address) {
	ptrSize := int64(s.bi.Arch.PtrSize())
	n := bitmap.ArrayLen()
	for i := int64(0); i < n; i++ {
		// The array uses 1 bit per word of heap. See mbitmap.go for
		// more information.
		m := bitmap.ArrayIndex(i).Uintptr()
		bits := 8 * ptrSize
		for j := int64(0); j < bits; j++ {
			if m>>uint(j)&1 != 0 {
				s.setHeapPtr(min.Add((i*bits + j) * ptrSize))
			}
		}
	}
}

// Read a multi-bit bitmap (Go 1.11-1.20), recording the heap pointers.
func (s *HeapScope) readMultiBitBitmap(bitmap *region, min Address) {
	ptrSize := int64(s.bi.Arch.PtrSize())
	n := bitmap.ArrayLen()
	for i := int64(0); i < n; i++ {
		// The nth byte is composed of 4 object bits and 4 live/dead
		// bits. We ignore the 4 live/dead bits, which are on the
		// high order side of the byte.
		//
		// See mbitmap.go for more information on the format of
		// the bitmap field of heapArena.
		m := bitmap.ArrayIndex(i).Uint8()
		for j := int64(0); j < 4; j++ {
			if m>>uint(j)&1 != 0 {
				s.setHeapPtr(min.Add((i*4 + j) * ptrSize))
			}
		}
	}
}

func (s *HeapScope) readSpans(mheap *region, arenas []arena) error {
	pageSize := s.rtConstant("_PageSize")
	// Span types
	spanInUse := uint8(s.rtConstant("_MSpanInUse"))
	if spanInUse == 0 {
		spanInUse = uint8(s.rtConstant("mSpanInUse"))
	}

	// Process spans.
	if pageSize%heapInfoSize != 0 {
		return fmt.Errorf("page size not a multiple of %d", heapInfoSize)
	}
	allspans := mheap.Field("allspans")

	n := allspans.SliceLen()
	for i := int64(0); i < n; i++ {
		sp := allspans.SliceIndex(i).Deref()
		min := Address(sp.Field("startAddr").Uintptr())
		elemSize := int64(sp.Field("elemsize").Uintptr())
		nPages := int64(sp.Field("npages").Uintptr())
		spanSize := nPages * pageSize
		max := min.Add(spanSize)
		st := sp.Field("state")
		if st.IsStruct() && st.HasField("s") { // go1.14+
			st = st.Field("s")
		}
		if st.IsStruct() && st.HasField("value") { // go1.20+
			st = st.Field("value")
		}
		switch st.Uint8() {
		case spanInUse:
			// initialize heap info records for all inuse spans.
			for a := min; a < max; a += heapInfoSize {
				h := s.allocHeapInfo(a)
				h.base = min
				h.size = elemSize
			}

			// Process special records.
			for special := sp.Field("specials"); special.Address() != 0; special = special.Field("next") {
				special = special.Deref() // *special to special
				if special.Field("kind").Uint8() != uint8(s.rtConstant("_KindSpecialFinalizer")) {
					// All other specials (just profile records) can't point into the heap.
					continue
				}
				obj := min.Add(int64(special.Field("offset").Uint16()))
				spty, _ := special.bi.findType("runtime.specialfinalizer")
				s.specials = append(s.specials,
					newVariable(fmt.Sprintf("finalizer for %x", obj), uint64(special.a), spty, special.bi, special.mem))
				// TODO: these aren't really "globals", as they
				// are kept alive by the object they reference being alive.
				// But we have no way of adding edges from an object to
				// the corresponding finalizer data, so we punt on that thorny
				// issue for now.
			}
		}
	}
	return nil
}

func (s *HeapScope) rtConstant(name string) int64 {
	x, _ := s.scope.findGlobal("runtime", name)
	if x != nil {
		v, _ := constant.Int64Val(x.Value)
		return v
	}
	return 0
}
