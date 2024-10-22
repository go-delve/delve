package proc

import (
	"errors"
	"fmt"
	"reflect"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/goversion"
)

type mapIterator interface {
	next() bool
	key() *Variable
	value() *Variable
}

func (v *Variable) mapIterator(maxNumBuckets uint64) mapIterator {
	mt := v.RealType.(*godwarf.MapType)
	sv := v.clone()
	sv.RealType = resolveTypedef(&(sv.RealType.(*godwarf.MapType).TypedefType))
	sv = sv.maybeDereference()
	v.Base = sv.Addr

	maptype, ok := sv.RealType.(*godwarf.StructType)
	if !ok {
		v.Unreadable = errors.New("wrong real type for map")
		return nil
	}

	it := &mapIteratorClassic{v: v, bidx: 0, b: nil, idx: 0, maxNumBuckets: maxNumBuckets}
	itswiss := &mapIteratorSwiss{v: v, maxNumGroups: maxNumBuckets, keyType: mt.KeyType, fieldType: mt.ElemType}
	var swisstyp *Variable

	if sv.Addr == 0 {
		it.numbuckets = 0
		return it
	}

	v.mem = cacheMemory(v.mem, v.Base, int(v.RealType.Size()))

	for _, f := range maptype.Field {
		var err error
		field, _ := sv.toField(f)
		switch f.Name {
		// Classic map fields
		case "count": // +rtype -fieldof hmap int
			v.Len, err = field.asInt()
		case "B": // +rtype -fieldof hmap uint8
			var b uint64
			b, err = field.asUint()
			it.numbuckets = 1 << b
			it.oldmask = (1 << (b - 1)) - 1
		case "buckets": // +rtype -fieldof hmap unsafe.Pointer
			it.buckets = field.maybeDereference()
		case "oldbuckets": // +rtype -fieldof hmap unsafe.Pointer
			it.oldbuckets = field.maybeDereference()

		// Swisstable map fields
		case "used":
			var n uint64
			n, err = field.asUint()
			v.Len = int64(n)
		case "directory":
			itswiss.directory = field.maybeDereference()
		case "typ":
			swisstyp = field.maybeDereference()
		}
		if err != nil {
			v.Unreadable = err
			return nil
		}
	}

	if it.buckets == nil && itswiss.directory != nil {
		if itswiss.directory.Kind != reflect.Slice {
			v.Unreadable = errMapDirectoryNotSlice
			return nil
		}
		if swisstyp == nil {
			v.Unreadable = errMapNoTyp
			return nil
		}
		err := itswiss.loadType(swisstyp)
		if err != nil {
			v.Unreadable = err
			return nil
		}
		return itswiss
	}

	if it.buckets.Kind != reflect.Struct || it.oldbuckets.Kind != reflect.Struct {
		v.Unreadable = errMapBucketsNotStruct
		return nil
	}

	it.hashTophashEmptyOne = hashTophashEmptyZero
	it.hashMinTopHash = hashMinTopHashGo111
	if producer := v.bi.Producer(); producer != "" && goversion.ProducerAfterOrEqual(producer, 1, 12) {
		it.hashTophashEmptyOne = hashTophashEmptyOne
		it.hashMinTopHash = hashMinTopHashGo112
	}

	return it
}

// Classic Maps ///////////////////////////////////////////////////////////////

type mapIteratorClassic struct {
	v          *Variable
	numbuckets uint64
	oldmask    uint64
	buckets    *Variable
	oldbuckets *Variable
	b          *Variable
	bidx       uint64

	tophashes *Variable
	keys      *Variable
	values    *Variable
	overflow  *Variable

	maxNumBuckets uint64 // maximum number of buckets to scan

	idx int64

	hashTophashEmptyOne uint64 // Go 1.12 and later has two sentinel tophash values for an empty cell, this is the second one (the first one hashTophashEmptyZero, the same as Go 1.11 and earlier)
	hashMinTopHash      uint64 // minimum value of tophash for a cell that isn't either evacuated or empty
}

var errMapBucketContentsNotArray = errors.New("malformed map type: keys, values or tophash of a bucket is not an array")
var errMapBucketContentsInconsistentLen = errors.New("malformed map type: inconsistent array length in bucket")
var errMapBucketsNotStruct = errors.New("malformed map type: buckets, oldbuckets or overflow field not a struct")

func (it *mapIteratorClassic) nextBucket() bool {
	if it.overflow != nil && it.overflow.Addr > 0 {
		it.b = it.overflow
	} else {
		it.b = nil

		if it.maxNumBuckets > 0 && it.bidx >= it.maxNumBuckets {
			return false
		}

		for it.bidx < it.numbuckets {
			it.b = it.buckets.clone()
			it.b.Addr += uint64(it.buckets.DwarfType.Size()) * it.bidx

			if it.oldbuckets.Addr <= 0 {
				break
			}

			// if oldbuckets is not nil we are iterating through a map that is in
			// the middle of a grow.
			// if the bucket we are looking at hasn't been filled in we iterate
			// instead through its corresponding "oldbucket" (i.e. the bucket the
			// elements of this bucket are coming from) but only if this is the first
			// of the two buckets being created from the same oldbucket (otherwise we
			// would print some keys twice)

			oldbidx := it.bidx & it.oldmask
			oldb := it.oldbuckets.clone()
			oldb.Addr += uint64(it.oldbuckets.DwarfType.Size()) * oldbidx

			if it.mapEvacuated(oldb) {
				break
			}

			if oldbidx == it.bidx {
				it.b = oldb
				break
			}

			// oldbucket origin for current bucket has not been evacuated but we have already
			// iterated over it so we should just skip it
			it.b = nil
			it.bidx++
		}

		if it.b == nil {
			return false
		}
		it.bidx++
	}

	if it.b.Addr <= 0 {
		return false
	}

	it.b.mem = cacheMemory(it.b.mem, it.b.Addr, int(it.b.RealType.Size()))

	it.tophashes = nil
	it.keys = nil
	it.values = nil
	it.overflow = nil

	for _, f := range it.b.DwarfType.(*godwarf.StructType).Field {
		field, err := it.b.toField(f)
		if err != nil {
			it.v.Unreadable = err
			return false
		}
		if field.Unreadable != nil {
			it.v.Unreadable = field.Unreadable
			return false
		}

		switch f.Name {
		case "tophash": // +rtype -fieldof bmap [8]uint8
			it.tophashes = field
		case "keys":
			it.keys = field
		case "values":
			it.values = field
		case "overflow":
			it.overflow = field.maybeDereference()
		}
	}

	// sanity checks
	if it.tophashes == nil || it.keys == nil || it.values == nil {
		it.v.Unreadable = errors.New("malformed map type")
		return false
	}

	if it.tophashes.Kind != reflect.Array || it.keys.Kind != reflect.Array || it.values.Kind != reflect.Array {
		it.v.Unreadable = errMapBucketContentsNotArray
		return false
	}

	if it.tophashes.Len != it.keys.Len {
		it.v.Unreadable = errMapBucketContentsInconsistentLen
		return false
	}

	if it.values.fieldType.Size() > 0 && it.tophashes.Len != it.values.Len {
		// if the type of the value is zero-sized (i.e. struct{}) then the values
		// array's length is zero.
		it.v.Unreadable = errMapBucketContentsInconsistentLen
		return false
	}

	if it.overflow.Kind != reflect.Struct {
		it.v.Unreadable = errMapBucketsNotStruct
		return false
	}

	return true
}

func (it *mapIteratorClassic) next() bool {
	for {
		if it.b == nil || it.idx >= it.tophashes.Len {
			r := it.nextBucket()
			if !r {
				return false
			}
			it.idx = 0
		}
		tophash, _ := it.tophashes.sliceAccess(int(it.idx))
		h, err := tophash.asUint()
		if err != nil {
			it.v.Unreadable = fmt.Errorf("unreadable tophash: %v", err)
			return false
		}
		it.idx++
		if h != hashTophashEmptyZero && h != it.hashTophashEmptyOne {
			return true
		}
	}
}

func (it *mapIteratorClassic) key() *Variable {
	k, _ := it.keys.sliceAccess(int(it.idx - 1))
	return k
}

func (it *mapIteratorClassic) value() *Variable {
	if it.values.fieldType.Size() <= 0 {
		return it.v.newVariable("", it.values.Addr, it.values.fieldType, DereferenceMemory(it.v.mem))
	}

	v, _ := it.values.sliceAccess(int(it.idx - 1))
	return v
}

func (it *mapIteratorClassic) mapEvacuated(b *Variable) bool {
	if b.Addr == 0 {
		return true
	}
	for _, f := range b.DwarfType.(*godwarf.StructType).Field {
		if f.Name != "tophash" {
			continue
		}
		tophashes, _ := b.toField(f)
		tophash0var, _ := tophashes.sliceAccess(0)
		tophash0, err := tophash0var.asUint()
		if err != nil {
			return true
		}
		//TODO: this needs to be > hashTophashEmptyOne for go >= 1.12
		return tophash0 > it.hashTophashEmptyOne && tophash0 < it.hashMinTopHash
	}
	return true
}

// Swisstable Maps ///////////////////////////////////////////////////////////////

const (
	swissMapGroupSlots         = 8                                // +rtype go1.24 internal/abi.SwissMapGroupSlots
	swissTableCtrlEmpty        = 0b10000000                       // +rtype go1.24 internal/runtime/maps.ctrlEmpty
	swissTableGroupSlotsOffset = uint64(unsafe.Sizeof(uint64(0))) // tracks internal/runtime/maps.groupSlotsOffset
)

type mapIteratorSwiss struct {
	v            *Variable
	directory    *Variable
	maxNumGroups uint64 // Maximum number of groups we will visit

	keyType, fieldType           godwarf.Type // Key and element type from v's type
	slotSize, elemOff, groupSize uint64       // from the SwissMapType object associated with v

	dirIdx int64
	tab    *swissTable

	groupIdx uint64
	group    uint64 // current group address

	slotIdx uint32

	groupCount uint64 // Total count of visited groups except for current table

	curKey, curValue *Variable
}

type swissTable struct {
	index            int64
	groupsData       uint64
	groupsLengthMask uint64
}

var errMapDirectoryNotSlice = errors.New("malformed map type: directory not a slice")
var errMapNoTyp = errors.New("malformed map type: no typ field on a swiss map")
var errSwissTableCouldNotLoad = errors.New("could not load one of the tables")
var errSwissMapNoGroups = errors.New("swiss table type does not have groups field")
var errSwissMapTypeNoGroup = errors.New("SwissMapType type does not a have a group field")
var errSwissMapGroupTypeNoSize = errors.New("swiss map group type does not have Size_ field")

func (it *mapIteratorSwiss) loadType(swisstyp *Variable) error {
	swisstyptyp, ok := resolveTypedef(swisstyp.DwarfType).(*godwarf.StructType)
	if !ok {
		return fmt.Errorf("swiss map type not a struct: %v", swisstyp.DwarfType.String())
	}

	var group *Variable
	for _, f := range swisstyptyp.Field {
		field, _ := swisstyp.toField(f)
		var err error
		switch f.Name {
		case "SlotSize":
			it.slotSize, err = field.asUint()
		case "ElemOff":
			it.elemOff, err = field.asUint()
		case "Group":
			group = field
		}
		if err != nil {
			return fmt.Errorf("could not load swiss table, reading SwissMapType field %s: %v", f.Name, err)
		}
	}

	if group == nil {
		return errSwissMapTypeNoGroup
	}

	groupsz := group.loadFieldNamed("Size_")
	if groupsz == nil {
		return errSwissMapGroupTypeNoSize
	}
	var err error
	it.groupSize, err = groupsz.asUint()
	if err != nil {
		return fmt.Errorf("could not load swiss map group size: %v", err)
	}
	return nil
}

// derived from $GOROOT/src/internal/runtime/maps/table.go
func (it *mapIteratorSwiss) next() bool {
	for it.dirIdx < it.directory.Len {
		if it.tab == nil {
			it.loadCurrentTable()
			if it.tab == nil {
				return false
			}
			if it.tab.index != it.dirIdx {
				it.nextTable()
				continue
			}
		}

		for ; it.groupIdx <= it.tab.groupsLengthMask; it.nextGroup() {
			if it.maxNumGroups > 0 && it.groupIdx+it.groupCount >= it.maxNumGroups {
				return false
			}
			if it.group == 0 {
				it.loadCurrentGroup()
				if it.group == 0 {
					return false
				}
			}

			for ; it.slotIdx < swissMapGroupSlots; it.slotIdx++ {
				if it.slotIsEmpty(it.slotIdx) {
					continue
				}

				it.curKey = it.slotKey(it.slotIdx)
				it.curValue = it.slotValue(it.slotIdx)

				it.slotIdx++
				if it.slotIdx >= swissMapGroupSlots {
					it.nextGroup()
					it.slotIdx = 0
				}
				return true
			}

			it.slotIdx = 0

		}

		it.groupCount += it.groupIdx
		it.groupIdx = 0
		it.group = 0
		it.nextTable()
	}
	return false
}

func (it *mapIteratorSwiss) nextTable() {
	it.dirIdx++
	it.tab = nil
}

func (it *mapIteratorSwiss) nextGroup() {
	it.groupIdx++
	it.group = 0
}

// loadCurrentTable loads the table at index it.dirIdx into it.tab
func (it *mapIteratorSwiss) loadCurrentTable() {
	tab, err := it.directory.sliceAccess(int(it.dirIdx))
	if err != nil || tab == nil || tab.Unreadable != nil {
		it.v.Unreadable = errSwissTableCouldNotLoad
		return
	}

	tab = tab.maybeDereference()

	tabtyp, ok := tab.DwarfType.(*godwarf.StructType)
	if !ok {
		it.v.Unreadable = fmt.Errorf("swiss table type is not a struct: %s", tab.DwarfType.String())
		return
	}

	r := &swissTable{}
	var groups *Variable

	for _, f := range tabtyp.Field {
		field, _ := tab.toField(f)
		switch f.Name {
		case "index":
			r.index, err = field.asInt()
			if err != nil {
				it.v.Unreadable = fmt.Errorf("could not load swiss table index: %v", err)
				return
			}
		case "groups":
			groups = field
		}
	}

	if groups == nil {
		it.v.Unreadable = errSwissMapNoGroups
		return
	}

	groupstyp, ok := groups.DwarfType.(*godwarf.StructType)
	if !ok {
		it.v.Unreadable = fmt.Errorf("swiss table groups type is not a struct: %s", groups.DwarfType.String())
		return
	}

	for _, f := range groupstyp.Field {
		field, _ := groups.toField(f)
		switch f.Name {
		case "data":
			field.loadPtr()
			if field.Unreadable != nil {
				it.v.Unreadable = fmt.Errorf("could not load swiss table group data: %v", field.Unreadable)
				return
			}
			r.groupsData = field.Children[0].Addr
		case "lengthMask":
			r.groupsLengthMask, err = field.asUint()
			if err != nil {
				it.v.Unreadable = fmt.Errorf("could not load swiss table group lengthMask: %v", err)
				return
			}
		}
	}

	it.tab = r
}

// loadCurrentGroup loads the group at index it.groupIdx of it.tab into it.group
func (it *mapIteratorSwiss) loadCurrentGroup() {
	it.group = it.tab.groupsData + (it.groupIdx * it.groupSize)
}

func (it *mapIteratorSwiss) key() *Variable {
	return it.curKey
}

func (it *mapIteratorSwiss) value() *Variable {
	return it.curValue
}

func (it *mapIteratorSwiss) slotKey(k uint32) *Variable {
	return it.v.newVariable("", uint64(it.group)+swissTableGroupSlotsOffset+uint64(k)*it.slotSize, it.keyType, DereferenceMemory(it.v.mem))
}

func (it *mapIteratorSwiss) slotValue(k uint32) *Variable {
	return it.v.newVariable("", uint64(it.group)+swissTableGroupSlotsOffset+uint64(k)*it.slotSize+it.elemOff, it.fieldType, DereferenceMemory(it.v.mem))

}

func (it *mapIteratorSwiss) slotIsEmpty(k uint32) bool {
	//TODO: check that this hasn't changed after it's merged and the TODO is deleted
	n, err := readUintRaw(DereferenceMemory(it.v.mem), uint64(it.group)+uint64(k), 1)
	if err != nil {
		it.v.Unreadable = fmt.Errorf("could not read swiss table group control array: %v", err)
		return true
	}
	return n&swissTableCtrlEmpty == swissTableCtrlEmpty
}
