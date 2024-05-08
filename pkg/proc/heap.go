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

	mem MemoryReadWriter
	bi  *BinaryInfo
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

func (s *HeapScope) readHeap(scope *EvalScope) error {
	rdr := s.bi.Images[0].DwarfReader()
	if rdr == nil {
		return errors.New("error dwarf reader is nil")
	}

	s.pageTable = map[Address]*pageTableEntry{}

	tmp, err := scope.findGlobal("runtime", "mheap_")
	if err != nil {
		return err
	}

	mheap := tmp.toRegion()

	return s.readSpans(scope, mheap)
}

func (s *HeapScope) readSpans(scope *EvalScope, mheap *region) error {
	rtConstant := func(name string) int64 {
		x, _ := scope.findGlobal("runtime", name)
		if x != nil {
			v, _ := constant.Int64Val(x.Value)
			return v
		}
		return 0
	}
	pageSize := rtConstant("_PageSize")
	// Span types
	spanInUse := uint8(rtConstant("_MSpanInUse"))
	if spanInUse == 0 {
		spanInUse = uint8(rtConstant("mSpanInUse"))
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
				if special.Field("kind").Uint8() != uint8(rtConstant("_KindSpecialFinalizer")) {
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
