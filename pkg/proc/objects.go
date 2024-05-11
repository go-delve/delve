package proc

import (
	"fmt"
	"os"
	"reflect"
	"regexp"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
)

// TODO:
// 1. 优化性能；
// 2. 优化map实现（在同一个heap object上）
// 3. 优化profile展示；
// 5. 测试kitex 内存泄露；
// 6. 测试fork进程；
// 7. 优化代码实现；
// 8. 闭包打印（闭包字段offset）
// 9. interface类型
// 10. 内存读取问题
// 11. name = ""
// DereferenceMemory（组合内存要做fallback）
// 12. 识别尽可能多的类型
// 13. sync.Map类型
// 14. 使用多线程
// 15. 忽略runtime对象
// 16. 设置最大深度限制
// 17.

type ReferenceVariable struct {
	Addr     uint64
	Name     string
	RealType godwarf.Type

	Index uint64

	heapBase uint64
	heapSize int64

	inStack bool

	// node size
	size int64
	// node count
	count int64

	prev *ReferenceVariable
}

type ObjRefScope struct {
	*HeapScope

	pb *profileBuilder

	gr           *G
	stackVisited map[Address]bool
}

// todo:
// 1. 尽量识别所有能识别的类型
func (s *ObjRefScope) findObject(prev *ReferenceVariable, addr Address, typ godwarf.Type, mem MemoryReadWriter) (v *ReferenceVariable) {
	h := s.findHeapInfo(addr)
	if h == nil {
		// Not in Go heap
		if s.gr == nil || uint64(addr) < s.gr.stack.lo || uint64(addr) > s.gr.stack.hi {
			// Not in Go stack
			return nil
		}
		if s.stackVisited[addr] {
			return nil
		}
		s.stackVisited[addr] = true
		v = &ReferenceVariable{
			Addr:     uint64(addr),
			RealType: resolveTypedef(typ),
			inStack:  true,
		}
		return v
	}
	base := h.base.Add(addr.Sub(h.base) / h.size * h.size)
	// Check if object is marked.
	h = s.findHeapInfo(base)
	// Find mark bit
	b := uint64(base) % heapInfoSize / 8
	if h.mark&(uint64(1)<<b) != 0 { // already found
		return nil
	}
	h.mark |= uint64(1) << b
	v = &ReferenceVariable{
		Addr:     uint64(addr),
		RealType: resolveTypedef(typ),
		heapBase: uint64(base),
	}
	v.heapSize, v.size = h.size, h.size
	v.count += 1
	if addr > base || typ.Size() < h.size {
		s.finalMark = append(s.finalMark, func() {
			var size, count int64
			for i := int64(0); i < h.size; i += int64(s.bi.Arch.PtrSize()) {
				a := base.Add(i)
				// explicit traversal known type
				if a >= addr && a < addr.Add(typ.Size()) {
					continue
				}
				if !s.isPtrFromHeap(a) {
					continue
				}
				ptr, err := readUintRaw(mem, uint64(a), int64(s.bi.Arch.PtrSize()))
				if err != nil {
					continue
				}
				size_, count_ := s.markObject(Address(ptr), mem)
				size += size_
				count += count_
			}
			s.record(&ReferenceVariable{
				Name:  v.Name,
				size:  size,
				count: count,
				prev:  prev,
			})
		})
	}
	return v
}

func (s *HeapScope) markObject(addr Address, mem MemoryReadWriter) (size, count int64) {
	h := s.findHeapInfo(addr)
	if h == nil {
		return
	}
	base := h.base.Add(addr.Sub(h.base) / h.size * h.size)
	// Check if object is marked.
	h = s.findHeapInfo(base)
	// Find mark bit
	b := uint64(base) % heapInfoSize / 8
	if h.mark&(uint64(1)<<b) != 0 { // already found
		return
	}
	h.mark |= uint64(1) << b
	size, count = h.size, 1
	for i := int64(0); i < h.size; i += int64(s.bi.Arch.PtrSize()) {
		a := base.Add(i)
		if !s.isPtrFromHeap(a) {
			continue
		}
		ptr, err := readUintRaw(mem, uint64(a), int64(s.bi.Arch.PtrSize()))
		if err != nil {
			continue
		}
		size_, count_ := s.markObject(Address(ptr), mem)
		size += size_
		count += count_
	}
	return
}

// TODO:  还是有问题
func (s *ObjRefScope) isValidAddr(v *ReferenceVariable, addr Address) bool {
	if v.inStack {
		// stack variable
		return uint64(addr) >= s.gr.stack.lo && uint64(addr) <= s.gr.stack.hi
	} else {
		base := Address(v.heapBase)
		return addr >= base && addr < base.Add(v.heapSize)
	}
}

func (s *ObjRefScope) directBucketObject(addr Address, typ godwarf.Type) (v *ReferenceVariable) {
	v = &ReferenceVariable{
		Addr:     uint64(addr),
		RealType: resolveTypedef(typ),
		heapBase: uint64(addr),
		heapSize: typ.Size(),
	}
	v.size = typ.Size()
	return v
}

func (s *ObjRefScope) record(x *ReferenceVariable) {
	if x.size == 0 && x.count == 0 {
		return
	}
	var indexes []uint64
	tmp := x
	for tmp != nil {
		if tmp.Name == "" {
			tmp = tmp.prev
			continue
		}
		if tmp.Index == 0 {
			tmp.Index = uint64(s.pb.stringIndex(tmp.Name))
		}
		indexes = append(indexes, tmp.Index)
		tmp = tmp.prev
	}
	s.pb.addReference(indexes, x.count, x.size)
}

func (s *ObjRefScope) findRef(prev, x *ReferenceVariable, mem MemoryReadWriter) {
	switch typ := x.RealType.(type) {
	case *godwarf.PtrType:
		ptrval, err := readUintRaw(mem, x.Addr, int64(s.bi.Arch.PtrSize()))
		if err != nil {
			return
		}
		if y := s.findObject(prev, Address(ptrval), resolveTypedef(typ.Type), DereferenceMemory(mem)); y != nil {
			s.findRef(prev, y, DereferenceMemory(mem))
			// flatten reference
			x.size += y.size
			x.count += y.count
		}
	case *godwarf.VoidType:
		return
	case *godwarf.ChanType:
		ptrval, err := readUintRaw(mem, x.Addr, int64(s.bi.Arch.PtrSize()))
		if err != nil {
			return
		}
		if y := s.findObject(prev, Address(ptrval), resolveTypedef(typ.Type.(*godwarf.PtrType).Type), DereferenceMemory(mem)); y != nil {
			x.size += y.size
			x.count += y.count

			structType, ok := y.RealType.(*godwarf.StructType)
			if !ok {
				return
			}
			var zptrval, chanLen uint64
			for _, field := range structType.Field {
				switch field.Name {
				case "buf":
					zptrval, err = readUintRaw(DereferenceMemory(mem), uint64(Address(y.Addr).Add(field.ByteOffset)), int64(s.bi.Arch.PtrSize()))
					if err != nil {
						return
					}
				case "dataqsiz":
					chanLen, _ = readUintRaw(DereferenceMemory(mem), uint64(Address(y.Addr).Add(field.ByteOffset)), int64(s.bi.Arch.PtrSize()))
				}
			}
			if z := s.findObject(prev, Address(zptrval), fakeArrayType(chanLen, typ.ElemType), DereferenceMemory(mem)); z != nil {
				s.findRef(prev, z, DereferenceMemory(mem))
				x.size += z.size
				x.count += z.count
			}
		}
	case *godwarf.MapType:
		ptrval, err := readUintRaw(mem, x.Addr, int64(s.bi.Arch.PtrSize()))
		if err != nil {
			return
		}
		if y := s.findObject(prev, Address(ptrval), resolveTypedef(typ.Type.(*godwarf.PtrType).Type), DereferenceMemory(mem)); y != nil {
			x.size += y.size
			x.count += y.count

			xv := newVariable("", x.Addr, x.RealType, s.bi, mem)
			it := xv.mapIterator()
			if it == nil {
				return
			}
			var idx int
			for it.next() {
				tmp := it.key()
				if key := s.directBucketObject(Address(tmp.Addr), resolveTypedef(tmp.RealType)); key != nil {
					if !isPrimitiveType(key.RealType) {
						key.Name = fmt.Sprintf("key%d", idx)
						key.prev = prev
						s.findRef(key, key, DereferenceMemory(mem))
						s.record(key)
					} else {
						x.size += key.size
						x.count += key.count
					}
				}
				if it.values.fieldType.Size() > 0 {
					tmp = it.value()
				} else {
					tmp = xv.newVariable("", it.values.Addr, it.values.fieldType, DereferenceMemory(mem))
				}
				if val := s.directBucketObject(Address(tmp.Addr), resolveTypedef(tmp.RealType)); val != nil {
					if !isPrimitiveType(val.RealType) {
						val.Name = fmt.Sprintf("val%d", idx)
						val.prev = prev
						s.findRef(val, val, DereferenceMemory(mem))
						s.record(val)
					} else {
						x.size += val.size
						x.count += val.count
					}
				}
				idx++
			}
		}
	case *godwarf.StringType:
		strAddr, strLen, err := readStringInfo(mem, s.bi.Arch, x.Addr, typ)
		if err != nil {
			return
		}
		if y := s.findObject(prev, Address(strAddr), fakeArrayType(uint64(strLen), &godwarf.UintType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 1, Name: "byte", ReflectKind: reflect.Uint8}, BitSize: 8, BitOffset: 0}}), DereferenceMemory(mem)); y != nil {
			x.size += y.size
			x.count += y.count
		}
	case *godwarf.SliceType:
		// mem = cacheMemory(mem, x.Addr, int(typ.Size()))
		var base, cap_ uint64
		var err error
		for _, f := range typ.Field {
			switch f.Name {
			case sliceArrayFieldName:
				base, err = readUintRaw(mem, uint64(int64(x.Addr)+f.ByteOffset), f.Type.Size())
				if err != nil {
					return
				}
			case sliceCapFieldName:
				cap_, _ = readUintRaw(mem, uint64(int64(x.Addr)+f.ByteOffset), f.Type.Size())
			}
		}
		if y := s.findObject(prev, Address(base), fakeArrayType(cap_, typ.ElemType), DereferenceMemory(mem)); y != nil {
			s.findRef(prev, y, DereferenceMemory(mem))
			x.size += y.size
			x.count += y.count
		}
	case *godwarf.InterfaceType:
		xv := newVariable("", x.Addr, x.RealType, s.bi, mem)
		_type, data, _ := xv.readInterface()
		if data == nil {
			return
		}
		ptrval, err := readUintRaw(mem, data.Addr, int64(s.bi.Arch.PtrSize()))
		if err != nil || ptrval == 0 {
			return
		}
		if _type != nil {
			rtyp, kind, err := runtimeTypeToDIE(_type, data.Addr, s.mds)
			if err == nil {
				if kind&kindDirectIface == 0 {
					if _, isptr := resolveTypedef(rtyp).(*godwarf.PtrType); !isptr {
						rtyp = pointerTo(rtyp, s.bi.Arch)
					}
				}
				if ptrType, isPtr := resolveTypedef(rtyp).(*godwarf.PtrType); isPtr {
					if y := s.findObject(prev, Address(ptrval), resolveTypedef(ptrType.Type), DereferenceMemory(mem)); y != nil {
						s.findRef(prev, y, DereferenceMemory(mem))
						x.size += y.size
						x.count += y.count
					}
					return
				}
			}
		}
		if y := s.findObject(prev, Address(ptrval), new(godwarf.VoidType), DereferenceMemory(mem)); y != nil {
			x.size += y.size
			x.count += y.count
		}
	case *godwarf.StructType:
		typ = s.specialStructTypes(typ)
		// cache mem
		for _, field := range typ.Field {
			fieldAddr := Address(x.Addr).Add(field.ByteOffset)
			if !s.isValidAddr(x, fieldAddr) {
				break
			}
			if isPrimitiveType(field.Type) {
				continue
			}
			y := &ReferenceVariable{
				Addr:     uint64(fieldAddr),
				Name:     field.Name,
				RealType: resolveTypedef(field.Type),
				inStack:  x.inStack,
				heapBase: x.heapBase,
				heapSize: x.heapSize,
			}
			y.prev = prev
			s.findRef(y, y, mem)
			s.record(y)
		}
	case *godwarf.ArrayType:
		eType := resolveTypedef(typ.Type)
		if isPrimitiveType(eType) {
			return
		}
		for i := int64(0); i < typ.Count; i++ {
			elemAddr := Address(x.Addr).Add(i * eType.Size())
			if !s.isValidAddr(x, elemAddr) {
				break
			}
			y := &ReferenceVariable{
				Addr:     uint64(elemAddr),
				Name:     fmt.Sprintf("[%d]", i),
				RealType: eType,
				inStack:  x.inStack,
				heapBase: x.heapBase,
				heapSize: x.heapSize,
			}
			y.prev = prev
			s.findRef(y, y, mem)
			s.record(y)
		}
	case *godwarf.FuncType:
		closureAddr, err := readUintRaw(mem, x.Addr, int64(s.bi.Arch.PtrSize()))
		if err != nil || closureAddr == 0 {
			return
		}
		funcAddr, err := readUintRaw(DereferenceMemory(mem), closureAddr, int64(s.bi.Arch.PtrSize()))
		if err == nil && funcAddr != 0 {
			if fn := s.bi.PCToFunc(funcAddr); fn != nil {
				cst := fn.closureStructType(s.bi)
				if closure := s.findObject(prev, Address(closureAddr), cst, DereferenceMemory(mem)); closure != nil {
					s.findRef(prev, closure, DereferenceMemory(mem))
					x.size += closure.size
					x.count += closure.count
				}
				return
			}
		}
		if closure := s.findObject(prev, Address(closureAddr), new(godwarf.VoidType), DereferenceMemory(mem)); closure != nil {
			x.size += closure.size
			x.count += closure.count
		}
	default:
		// TODO
	}
	return
}

var atomicPointerRegex = regexp.MustCompile(`^sync/atomic\.Pointer\[.*\]$`)

func (s *ObjRefScope) specialStructTypes(st *godwarf.StructType) *godwarf.StructType {
	switch {
	case atomicPointerRegex.MatchString(st.StructName):
		// v *sync.readOnly
		nst := *st
		nst.Field = make([]*godwarf.StructField, len(st.Field))
		for j := range st.Field {
			nst.Field[j] = st.Field[j]
		}
		nf := *nst.Field[2]
		nf.Type = nst.Field[0].Type.(*godwarf.ArrayType).Type
		nst.Field[2] = &nf
		return &nst
	}
	return st
}

func isPrimitiveType(typ godwarf.Type) bool {
	typ = resolveTypedef(typ)
	switch typ.(type) {
	case *godwarf.BoolType, *godwarf.FloatType, *godwarf.UintType,
		*godwarf.UcharType, *godwarf.CharType, *godwarf.IntType, *godwarf.ComplexType:
		return true
	}
	return false
}

func (t *Target) ObjectReference(filename string) error {
	scope, err := ThreadScope(t, t.CurrentThread())
	if err != nil {
		return err
	}

	heapScope := &HeapScope{mem: t.Memory(), bi: t.BinInfo(), scope: scope}
	err = heapScope.readHeap()
	if err != nil {
		return err
	}

	f, err := os.Create(filename)
	if err != nil {
		return err
	}

	ors := &ObjRefScope{
		HeapScope: heapScope,
		pb:        newProfileBuilder(f),
	}

	mds, err := loadModuleData(t.BinInfo(), t.Memory())
	if err != nil {
		return err
	}
	ors.mds = mds

	grs, _, _ := GoroutinesInfo(t, 0, 0)
	for _, gr := range grs {
		sf, _ := GoroutineStacktrace(t, gr, 512, 0)
		if len(sf) > 0 {
			ors.gr = gr
			ors.stackVisited = make(map[Address]bool)
			for i := range sf {
				scope, _ := ConvertEvalScope(t, gr.ID, i, 0)
				locals, _ := scope.LocalVariables(loadSingleValue)
				for _, l := range locals {
					if l.Addr != 0 {
						root := &ReferenceVariable{
							Addr:     l.Addr,
							Name:     sf[i].Current.Fn.Name + "." + l.Name,
							RealType: l.RealType,
							inStack:  true,
						}
						ors.findRef(root, root, l.mem)
						ors.record(root)
					}
				}
			}
		}
	}

	ors.gr = nil
	ors.stackVisited = nil
	pvs, _ := scope.PackageVariables(loadSingleValue)
	for _, pv := range pvs {
		if pv.Addr != 0 {
			root := &ReferenceVariable{
				Addr:     pv.Addr,
				Name:     pv.Name,
				RealType: pv.RealType,
				heapBase: pv.Addr,
				heapSize: pv.RealType.Size(),
			}
			ors.findRef(root, root, t.Memory())
			ors.record(root)
		}
	}

	// Finalizers
	for _, r := range heapScope.specials {
		for _, child := range r.Children {
			if child.Addr != 0 {
				root := &ReferenceVariable{
					Addr:     child.Addr,
					Name:     child.Name,
					RealType: child.RealType,
					heapBase: child.Addr,
					heapSize: child.RealType.Size(),
				}
				ors.findRef(root, root, t.Memory())
				ors.record(root)
			}
		}
	}

	for _, mark := range heapScope.finalMark {
		mark()
	}

	ors.pb.flush()
	return nil
}
