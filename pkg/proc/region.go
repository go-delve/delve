// Copyright 2017 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package proc

import (
	"github.com/go-delve/delve/pkg/dwarf/godwarf"
)

func (v *Variable) toRegion() *region {
	return &region{
		mem: v.mem,
		bi:  v.bi,
		a:   Address(v.Addr),
		typ: v.RealType,
	}
}

// A region is a piece of the virtual address space of the inferior.
// It has an address and a type.
// Note that it is the type of the thing in the region,
// not the type of the reference to the region.
type region struct {
	mem MemoryReadWriter
	bi  *BinaryInfo
	a   Address
	typ godwarf.Type
}

func (r *region) clone() *region {
	nr := *r
	return &nr
}

// Address returns the address that a region of pointer type points to.
func (r *region) Address() Address {
	switch t := r.typ.(type) {
	case *godwarf.PtrType:
		ptr, _ := readUintRaw(r.mem, uint64(r.a), t.Size())
		return Address(ptr)
	default:
		panic("can't ask for the Address of a non-pointer " + t.String())
	}
}

// Int returns the int value stored in r.
func (r *region) Int() int64 {
	switch t := r.typ.(type) {
	case *godwarf.IntType:
		if t.Size() != int64(r.bi.Arch.PtrSize()) {
			panic("not an int: " + t.String())
		}
		i, _ := readIntRaw(r.mem, uint64(r.a), t.Size())
		return i
	default:
		panic("not an int: " + t.String())
	}
}

// Uintptr returns the uintptr value stored in r.
func (r *region) Uintptr() uint64 {
	switch t := r.typ.(type) {
	case *godwarf.UintType:
		if t.Size() != int64(r.bi.Arch.PtrSize()) {
			panic("not an uintptr: " + t.String())
		}
		i, _ := readUintRaw(r.mem, uint64(r.a), t.Size())
		return i
	default:
		panic("not a uintptr: " + t.String())
	}
}

// Deref loads from a pointer. r must contain a pointer.
func (r *region) Deref() *region {
	switch t := r.typ.(type) {
	case *godwarf.PtrType:
		ptr, _ := readUintRaw(r.mem, uint64(r.a), t.Size())
		re := &region{bi: r.bi, a: Address(ptr), typ: resolveTypedef(t.Type)}
		re.mem = cacheMemory(r.mem, uint64(re.a), int(re.typ.Size()))
		return re
	default:
		panic("can't deref on non-pointer: " + t.String())
	}
}

// Uint64 returns the uint64 value stored in r.
// r must have type uint64.
func (r *region) Uint64() uint64 {
	switch t := r.typ.(type) {
	case *godwarf.UintType:
		if t.Size() != 8 {
			panic("bad uint64 type " + t.String())
		}
		i, _ := readUintRaw(r.mem, uint64(r.a), t.Size())
		return i
	default:
		panic("bad uint64 type " + t.String())
	}
}

// Uint32 returns the uint32 value stored in r.
// r must have type uint32.
func (r *region) Uint32() uint32 {
	switch t := r.typ.(type) {
	case *godwarf.UintType:
		if t.Size() != 4 {
			panic("bad uint32 type " + t.String())
		}
		i, _ := readUintRaw(r.mem, uint64(r.a), t.Size())
		return uint32(i)
	default:
		panic("bad uint32 type " + t.String())
	}
}

// Int32 returns the int32 value stored in r.
// r must have type int32.
func (r *region) Int32() int32 {
	switch t := r.typ.(type) {
	case *godwarf.IntType:
		if t.Size() != 4 {
			panic("bad int32 type " + t.String())
		}
		i, _ := readIntRaw(r.mem, uint64(r.a), t.Size())
		return int32(i)
	default:
		panic("bad int32 type " + t.String())
	}
}

// Uint16 returns the uint16 value stored in r.
// r must have type uint16.
func (r *region) Uint16() uint16 {
	switch t := r.typ.(type) {
	case *godwarf.UintType:
		if t.Size() != 2 {
			panic("bad uint16 type " + t.String())
		}
		i, _ := readUintRaw(r.mem, uint64(r.a), t.Size())
		return uint16(i)
	default:
		panic("bad uint16 type " + t.String())
	}
}

// Uint8 returns the uint8 value stored in r.
// r must have type uint8.
func (r *region) Uint8() uint8 {
	switch t := r.typ.(type) {
	case *godwarf.UintType:
		if t.Size() != 1 {
			panic("bad uint8 type " + t.String())
		}
		i, _ := readUintRaw(r.mem, uint64(r.a), t.Size())
		return uint8(i)
	default:
		panic("bad uint8 type " + t.String())
	}
}

// Bool returns the bool value stored in r.
// r must have type bool.
func (r *region) Bool() bool {
	switch t := r.typ.(type) {
	case *godwarf.BoolType:
		i, _ := readUintRaw(r.mem, uint64(r.a), t.Size())
		return uint8(i) != 0
	default:
		panic("bad bool type " + r.typ.String())
	}
}

// String returns the value of the string stored in r.
func (r *region) String() string {
	switch t := r.typ.(type) {
	case *godwarf.StringType:
		ptrSize := int64(r.bi.Arch.PtrSize())
		p, _ := readUintRaw(r.mem, uint64(r.a), ptrSize)
		n, _ := readUintRaw(r.mem, uint64(r.a.Add(ptrSize)), ptrSize)
		b := make([]byte, n)
		r.mem.ReadMemory(b, p)
		return string(b)
	default:
		panic("bad string type " + t.String())
	}
}

// SliceIndex indexes a slice (a[n]). r must contain a slice.
// n must be in bounds for the slice.
func (r *region) SliceIndex(n int64) *region {
	switch t := r.typ.(type) {
	case *godwarf.SliceType:
		ptrSize := int64(r.bi.Arch.PtrSize())
		p, _ := readUintRaw(r.mem, uint64(r.a), ptrSize)
		re := &region{bi: r.bi, a: Address(p).Add(n * t.ElemType.Size()), typ: resolveTypedef(t.ElemType), mem: r.mem}
		return re
	default:
		panic("can't index a non-slice")
	}
}

// SliceLen returns the length of a slice. r must contain a slice.
func (r *region) SliceLen() int64 {
	switch r.typ.(type) {
	case *godwarf.SliceType:
		ptrSize := int64(r.bi.Arch.PtrSize())
		p, _ := readIntRaw(r.mem, uint64(r.a.Add(ptrSize)), ptrSize)
		return p
	default:
		panic("can't len a non-slice")
	}
}

// SliceCap returns the capacity of a slice. r must contain a slice.
func (r *region) SliceCap() int64 {
	switch r.typ.(type) {
	case *godwarf.SliceType:
		ptrSize := int64(r.bi.Arch.PtrSize())
		p, _ := readIntRaw(r.mem, uint64(r.a.Add(ptrSize*2)), ptrSize)
		return p
	default:
		panic("can't cap a non-slice")
	}
}

// Field returns the part of r which contains the field f.
// r must contain a struct, and f must be one of its fields.
func (r *region) Field(fn string) *region {
	switch t := r.typ.(type) {
	case *godwarf.StructType:
		for _, f := range t.Field {
			if f.Name == fn {
				re := &region{bi: r.bi, a: r.a.Add(f.ByteOffset), typ: resolveTypedef(f.Type), mem: r.mem}
				return re
			}
		}
	}
	panic("can't find field " + r.typ.String() + "." + fn)
}

func (r *region) HasField(fn string) bool {
	switch t := r.typ.(type) {
	case *godwarf.StructType:
		for _, f := range t.Field {
			if f.Name == fn {
				return true
			}
		}
	}
	return false
}

func (r *region) Array() *region {
	switch t := r.typ.(type) {
	case *godwarf.SliceType:
		ptrSize := int64(r.bi.Arch.PtrSize())
		p, _ := readUintRaw(r.mem, uint64(r.a), ptrSize)
		c, _ := readUintRaw(r.mem, uint64(r.a.Add(ptrSize)), ptrSize)
		re := &region{bi: r.bi, a: Address(p), typ: fakeArrayType(c, resolveTypedef(t.ElemType))}
		re.mem = cacheMemory(r.mem, p, int(re.typ.Size()))
		return re
	default:
		panic("can't deref a non-slice")
	}
}

func (r *region) ArrayLen() int64 {
	switch t := r.typ.(type) {
	case *godwarf.ArrayType:
		return t.Count
	default:
		panic("can't ArrayLen a non-array")
	}
}

func (r *region) ArrayIndex(i int64, to *region) {
	switch t := r.typ.(type) {
	case *godwarf.ArrayType:
		if i < 0 || i >= t.Count {
			panic("array index out of bounds")
		}
		to.mem = r.mem
		to.bi = r.bi
		to.a = r.a.Add(i * t.Type.Size())
		to.typ = resolveTypedef(t.Type)
		return
	default:
		panic("can't ArrayLen a non-array")
	}
}

func (r *region) ArrayElemType() godwarf.Type {
	switch t := r.typ.(type) {
	case *godwarf.ArrayType:
		return t.Type
	default:
		panic("can't ArrayElemType a non-array")
	}
}

func (r *region) IsStruct() bool {
	_, ok := r.typ.(*godwarf.StructType)
	return ok
}

func (r *region) IsArray() bool {
	_, ok := r.typ.(*godwarf.ArrayType)
	return ok
}

func (r *region) IsUint16() bool {
	t, ok := r.typ.(*godwarf.UintType)
	return ok && t.Size() == 2
}
