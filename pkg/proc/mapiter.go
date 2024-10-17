package proc

import (
	"errors"
	"fmt"
	"reflect"

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

	isptr := func(typ godwarf.Type) bool {
		_, isptr := typ.(*godwarf.PtrType)
		return isptr
	}

	it := &mapIteratorClassic{v: v, bidx: 0, b: nil, idx: 0, maxNumBuckets: maxNumBuckets, keyTypeIsPtr: isptr(mt.KeyType), elemTypeIsPtr: isptr(mt.ElemType)}
	itswiss := &mapIteratorSwiss{v: v, maxNumGroups: maxNumBuckets, keyTypeIsPtr: isptr(mt.KeyType), elemTypeIsPtr: isptr(mt.ElemType)}

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
		case "dirPtr":
			itswiss.dirPtr = field
		case "dirLen":
			itswiss.dirLen, err = field.asInt()
		}
		if err != nil {
			v.Unreadable = err
			return nil
		}
	}

	if it.buckets == nil && itswiss.dirPtr != nil {
		itswiss.loadTypes()
		return itswiss
	}

	if it.buckets == nil || it.oldbuckets == nil || it.buckets.Kind != reflect.Struct || it.oldbuckets.Kind != reflect.Struct {
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

	keyTypeIsPtr, elemTypeIsPtr bool

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
	if k.Kind == reflect.Ptr && !it.keyTypeIsPtr {
		k = k.maybeDereference()
	}
	return k
}

func (it *mapIteratorClassic) value() *Variable {
	if it.values.fieldType.Size() <= 0 {
		return it.v.newVariable("", it.values.Addr, it.values.fieldType, DereferenceMemory(it.v.mem))
	}

	v, _ := it.values.sliceAccess(int(it.idx - 1))
	if v.Kind == reflect.Ptr && !it.elemTypeIsPtr {
		v = v.maybeDereference()
	}

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
	swissTableCtrlEmpty = 0b10000000 // +rtype go1.24 internal/runtime/maps.ctrlEmpty
)

type mapIteratorSwiss struct {
	v            *Variable
	dirPtr       *Variable
	dirLen       int64
	maxNumGroups uint64 // Maximum number of groups we will visit

	keyTypeIsPtr, elemTypeIsPtr bool
	tableType, groupType        *godwarf.StructType

	tableFieldIndex, tableFieldGroups, groupsFieldLengthMask, groupsFieldData, groupFieldCtrl, groupFieldSlots, slotFieldKey, slotFieldElem *godwarf.StructField

	dirIdx int64
	tab    *swissTable

	groupIdx uint64
	group    *swissGroup

	slotIdx uint32

	groupCount uint64 // Total count of visited groups except for current table

	curKey, curValue *Variable
}

type swissTable struct {
	index  int64
	groups *Variable
}

type swissGroup struct {
	slots *Variable
	ctrls []byte
}

var errSwissTableCouldNotLoad = errors.New("could not load one of the tables")
var errSwissMapBadType = errors.New("swiss table type does not have some required fields")
var errSwissMapBadTableField = errors.New("swiss table bad table field")
var errSwissMapBadGroupTypeErr = errors.New("bad swiss map type, group type lacks some required fields")

// loadTypes determines the correct type for it.dirPtr:  the linker records
// this type as **table but in reality it is either *[dirLen]*table for
// large maps or *group for small maps, when it.dirLen == 0.
func (it *mapIteratorSwiss) loadTypes() {
	tableptrptrtyp, ok := it.dirPtr.DwarfType.(*godwarf.PtrType)
	if !ok {
		it.v.Unreadable = errSwissMapBadTableField
		return
	}
	tableptrtyp, ok := tableptrptrtyp.Type.(*godwarf.PtrType)
	if !ok {
		it.v.Unreadable = errSwissMapBadTableField
		return
	}
	it.tableType, ok = tableptrtyp.Type.(*godwarf.StructType)
	if !ok {
		it.v.Unreadable = errSwissMapBadTableField
		return
	}
	for _, field := range it.tableType.Field {
		switch field.Name {
		case "index":
			it.tableFieldIndex = field
		case "groups":
			it.tableFieldGroups = field
			groupstyp, ok := field.Type.(*godwarf.StructType)
			if ok {
				for _, field := range groupstyp.Field {
					switch field.Name {
					case "data":
						it.groupsFieldData = field
						typ, ok := field.Type.(*godwarf.PtrType)
						if ok {
							it.groupType, _ = resolveTypedef(typ.Type).(*godwarf.StructType)
						}
					case "lengthMask":
						it.groupsFieldLengthMask = field
					}
				}
			}
		}
	}
	if it.groupType == nil || it.tableFieldIndex == nil || it.tableFieldGroups == nil || it.groupsFieldLengthMask == nil {
		it.v.Unreadable = errSwissMapBadType
		return
	}
	for _, field := range it.groupType.Field {
		switch field.Name {
		case "ctrl":
			it.groupFieldCtrl = field
		case "slots":
			it.groupFieldSlots = field
		}
	}
	if it.groupFieldCtrl == nil || it.groupFieldSlots == nil {
		it.v.Unreadable = errSwissMapBadGroupTypeErr
		return
	}

	slotsType, ok := resolveTypedef(it.groupFieldSlots.Type).(*godwarf.ArrayType)
	if !ok {
		it.v.Unreadable = errSwissMapBadGroupTypeErr
		return
	}
	slotType, ok := slotsType.Type.(*godwarf.StructType)
	if !ok {
		it.v.Unreadable = errSwissMapBadGroupTypeErr
		return
	}
	for _, field := range slotType.Field {
		switch field.Name {
		case "key":
			it.slotFieldKey = field
		case "elem":
			it.slotFieldElem = field
		}
	}
	if it.slotFieldKey == nil || it.slotFieldElem == nil {
		it.v.Unreadable = errSwissMapBadGroupTypeErr
		return
	}

	if it.dirLen <= 0 {
		// small maps, convert it.dirPtr to be of type *group, then dereference it
		it.dirPtr.DwarfType = pointerTo(fakeArrayType(1, it.groupType), it.v.bi.Arch)
		it.dirPtr.RealType = it.dirPtr.DwarfType
		it.dirPtr = it.dirPtr.maybeDereference()
		it.dirLen = 1
		it.tab = &swissTable{groups: it.dirPtr} // so that we don't try to load this later on
		return
	}

	// normal map, convert it.dirPtr to be of type *[dirLen]*table, then dereference it

	it.dirPtr.DwarfType = pointerTo(fakeArrayType(uint64(it.dirLen), tableptrtyp), it.v.bi.Arch)
	it.dirPtr.RealType = it.dirPtr.DwarfType
	it.dirPtr = it.dirPtr.maybeDereference()
}

// derived from $GOROOT/src/internal/runtime/maps/table.go and $GOROOT/src/runtime/runtime-gdb.py
func (it *mapIteratorSwiss) next() bool {
	if it.v.Unreadable != nil {
		return false
	}
	for it.dirIdx < it.dirLen {
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

		for ; it.groupIdx < uint64(it.tab.groups.Len); it.nextGroup() {
			if it.maxNumGroups > 0 && it.groupIdx+it.groupCount >= it.maxNumGroups {
				return false
			}
			if it.group == nil {
				it.loadCurrentGroup()
				if it.group == nil {
					return false
				}
			}

			for ; it.slotIdx < uint32(it.group.slots.Len); it.slotIdx++ {
				if it.slotIsEmptyOrDeleted(it.slotIdx) {
					continue
				}

				cur, err := it.group.slots.sliceAccess(int(it.slotIdx))
				if err != nil {
					it.v.Unreadable = fmt.Errorf("error accessing swiss map in table %d, group %d, slot %d", it.dirIdx, it.groupIdx, it.slotIdx)
					return false
				}

				var err1, err2 error
				it.curKey, err1 = cur.toField(it.slotFieldKey)
				it.curValue, err2 = cur.toField(it.slotFieldElem)
				if err1 != nil || err2 != nil {
					it.v.Unreadable = fmt.Errorf("error accessing swiss map slot: %v %v", err1, err2)
					return false
				}

				// If the type we expect is non-pointer but we read a pointer type it
				// means that the key (or the value) is stored indirectly into the map
				// because it is too big. We dereference it here so that the type of the
				// key (or value) matches the type on the map definition.
				if it.curKey.Kind == reflect.Ptr && !it.keyTypeIsPtr {
					it.curKey = it.curKey.maybeDereference()
				}
				if it.curValue.Kind == reflect.Ptr && !it.elemTypeIsPtr {
					it.curValue = it.curValue.maybeDereference()
				}

				it.slotIdx++
				return true
			}

			it.slotIdx = 0
		}

		it.groupCount += it.groupIdx
		it.groupIdx = 0
		it.group = nil
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
	it.group = nil
}

// loadCurrentTable loads the table at index it.dirIdx into it.tab
func (it *mapIteratorSwiss) loadCurrentTable() {
	tab, err := it.dirPtr.sliceAccess(int(it.dirIdx))
	if err != nil || tab == nil || tab.Unreadable != nil {
		it.v.Unreadable = errSwissTableCouldNotLoad
		return
	}

	tab = tab.maybeDereference()

	r := &swissTable{}

	field, _ := tab.toField(it.tableFieldIndex)
	r.index, err = field.asInt()
	if err != nil {
		it.v.Unreadable = fmt.Errorf("could not load swiss table index: %v", err)
		return
	}

	groups, _ := tab.toField(it.tableFieldGroups)
	r.groups, _ = groups.toField(it.groupsFieldData)

	field, _ = groups.toField(it.groupsFieldLengthMask)
	groupsLengthMask, err := field.asUint()
	if err != nil {
		it.v.Unreadable = fmt.Errorf("could not load swiss table group lengthMask: %v", err)
		return
	}

	// convert the type of groups from *group to *[len]group so that it's easier to use
	r.groups.DwarfType = pointerTo(fakeArrayType(groupsLengthMask+1, it.groupType), it.v.bi.Arch)
	r.groups.RealType = r.groups.DwarfType
	r.groups = r.groups.maybeDereference()

	it.tab = r
}

// loadCurrentGroup loads the group at index it.groupIdx of it.tab into it.group
func (it *mapIteratorSwiss) loadCurrentGroup() {
	group, err := it.tab.groups.sliceAccess(int(it.groupIdx))
	if err != nil {
		it.v.Unreadable = fmt.Errorf("could not load swiss map group: %v", err)
		return
	}
	g := &swissGroup{}
	g.slots, _ = group.toField(it.groupFieldSlots)
	ctrl, _ := group.toField(it.groupFieldCtrl)
	g.ctrls = make([]byte, ctrl.DwarfType.Size())
	_, err = ctrl.mem.ReadMemory(g.ctrls, ctrl.Addr)
	if err != nil {
		it.v.Unreadable = err
		return
	}
	it.group = g
}

func (it *mapIteratorSwiss) key() *Variable {
	return it.curKey
}

func (it *mapIteratorSwiss) value() *Variable {
	return it.curValue
}

func (it *mapIteratorSwiss) slotIsEmptyOrDeleted(k uint32) bool {
	//TODO: check that this hasn't changed after it's merged and the TODO is deleted
	return it.group.ctrls[k]&swissTableCtrlEmpty == swissTableCtrlEmpty
}
