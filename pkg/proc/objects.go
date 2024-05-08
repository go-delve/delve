package proc

import (
	"fmt"
	"os"
	"reflect"
	"regexp"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/logflags"
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
}

type ObjRefScope struct {
	*HeapScope

	pb *profileBuilder

	referenceLink []*ReferenceVariable

	gr           *G
	stackVisited map[Address]bool
}

// for testing
type FallbackMemory struct {
	MemoryReadWriter
}

func (m *FallbackMemory) ReadMemory(buf []byte, addr uint64) (n int, err error) {
	n, err = m.MemoryReadWriter.ReadMemory(buf, addr)
	if err == nil {
		return
	}
	if cmem, ok := m.MemoryReadWriter.(*compositeMemory); ok {
		return cmem.realmem.ReadMemory(buf, addr)
	}
	return
}

// todo:
// 1. 尽量识别所有能识别的类型
func (s *ObjRefScope) findObject(addr Address, typ godwarf.Type) (v *ReferenceVariable) {
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
	// mark each unknown type pointer
	for i := int64(0); i < h.size; i += int64(s.bi.Arch.PtrSize()) {
		a := base.Add(i)
		// explicit traversal known type
		if a >= addr && a < addr.Add(typ.Size()) {
			continue
		}
		if !s.isPtrFromHeap(a) {
			continue
		}
		ptr, _ := readUintRaw(s.mem, uint64(a), int64(s.bi.Arch.PtrSize()))
		if ptr > 0 {
			if sv := s.findObject(Address(ptr), &godwarf.VoidType{CommonType: godwarf.CommonType{ByteSize: int64(0)}}); sv != nil {
				v.size += sv.size
				v.count += sv.count
			}
		}
	}
	return v
}

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
	}
	v.size = typ.Size()
	return v
}

func (s *ObjRefScope) record(x *ReferenceVariable) {
	if x.size == 0 && x.count == 0 {
		return
	}
	var indexes []uint64
	if x.Index == 0 {
		x.Index = uint64(s.pb.stringIndex(x.Name))
	}
	indexes = append(indexes, x.Index)
	for i := len(s.referenceLink) - 1; i >= 0; i-- {
		y := s.referenceLink[i]
		if y.Index == 0 {
			y.Index = uint64(s.pb.stringIndex(y.Name))
		}
		indexes = append(indexes, y.Index)
	}
	s.pb.addReference(indexes, x.count, x.size)
}

func (s *ObjRefScope) fillRefs(x *ReferenceVariable, inStack bool) {
	if inStack {
		s.referenceLink = append(s.referenceLink, x)
		defer func() { s.referenceLink = s.referenceLink[:len(s.referenceLink)-1] }()
	}
	switch typ := x.RealType.(type) {
	case *godwarf.PtrType:
		ptrval, _ := readUintRaw(s.mem, x.Addr, int64(s.bi.Arch.PtrSize()))
		if ptrval != 0 {
			if y := s.findObject(Address(ptrval), resolveTypedef(typ.Type)); y != nil {
				s.fillRefs(y, false)
				// flatten reference
				x.size += y.size
				x.count += y.count
			}
		}
	case *godwarf.VoidType:
		return
	case *godwarf.ChanType:
		ptrval, _ := readUintRaw(s.mem, x.Addr, int64(s.bi.Arch.PtrSize()))
		if ptrval != 0 {
			if y := s.findObject(Address(ptrval), resolveTypedef(typ.Type.(*godwarf.PtrType).Type)); y != nil {
				x.size += y.size
				x.count += y.count

				structType, ok := y.RealType.(*godwarf.StructType)
				if !ok {
					logflags.DebuggerLogger().Errorf("bad channel type %v", y.RealType.String())
					return
				}
				chanLen, _ := readUintRaw(s.mem, uint64(Address(ptrval).Add(structType.Field[1].ByteOffset)), int64(s.bi.Arch.PtrSize()))

				if chanLen > 0 {
					for _, field := range structType.Field {
						if field.Name == "buf" {
							zptrval, _ := readUintRaw(s.mem, uint64(Address(y.Addr).Add(field.ByteOffset)), int64(s.bi.Arch.PtrSize()))
							if zptrval != 0 {
								if z := s.findObject(Address(zptrval), fakeArrayType(chanLen, typ.ElemType)); z != nil {
									s.fillRefs(z, false)
									x.size += z.size
									x.count += z.count
								}
							}
							break
						}
					}
				}
			}
		}
	case *godwarf.MapType:
		// todo: optimize implementation
		ptrval, _ := readUintRaw(s.mem, x.Addr, int64(s.bi.Arch.PtrSize()))
		if ptrval != 0 {
			if y := s.findObject(Address(ptrval), resolveTypedef(typ.Type.(*godwarf.PtrType).Type)); y != nil {
				x.size += y.size
				x.count += y.count

				xv := newVariable("", x.Addr, x.RealType, s.bi, s.mem)
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
							s.fillRefs(key, true)
							s.record(key)
						} else {
							x.size += key.size
							x.count += key.count
						}
					}
					if it.values.fieldType.Size() > 0 {
						tmp = it.value()
					} else {
						tmp = xv.newVariable("", it.values.Addr, it.values.fieldType, s.mem)
					}
					if val := s.directBucketObject(Address(tmp.Addr), resolveTypedef(tmp.RealType)); val != nil {
						if !isPrimitiveType(val.RealType) {
							val.Name = fmt.Sprintf("val%d", idx)
							s.fillRefs(val, true)
							s.record(val)
						} else {
							x.size += val.size
							x.count += val.count
						}
					}
					idx++
				}
			}
		}
	case *godwarf.StringType:
		strAddr, strLen, _ := readStringInfo(s.mem, s.bi.Arch, x.Addr, typ)
		if strLen > 0 {
			if y := s.findObject(Address(strAddr), fakeArrayType(uint64(strLen), &godwarf.UintType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 1, Name: "byte", ReflectKind: reflect.Uint8}, BitSize: 8, BitOffset: 0}})); y != nil {
				x.size += y.size
				x.count += y.count
			}
		}
	case *godwarf.SliceType:
		base, _ := readUintRaw(s.mem, x.Addr, int64(s.bi.Arch.PtrSize()))
		if base != 0 {
			cap_, _ := readUintRaw(s.mem, uint64(Address(x.Addr).Add(int64(s.bi.Arch.PtrSize())*2)), int64(s.bi.Arch.PtrSize()))
			if y := s.findObject(Address(base), fakeArrayType(cap_, typ.ElemType)); y != nil {
				s.fillRefs(y, false)
				x.size += y.size
				x.count += y.count
			}
		}
	case *godwarf.InterfaceType:
		xv := newVariable("", x.Addr, x.RealType, s.bi, s.mem)
		_type, data, isnil := xv.readInterface()
		if isnil || data == nil {
			return
		}
		rtyp, _, err := runtimeTypeToDIE(_type, data.Addr, s.mds)
		if err != nil {
			return
		}
		realtyp := resolveTypedef(rtyp)
		if ptrType, isPtr := realtyp.(*godwarf.PtrType); isPtr {
			ptrval, _ := readUintRaw(s.mem, data.Addr, int64(s.bi.Arch.PtrSize()))
			if ptrval != 0 {
				if y := s.findObject(Address(ptrval), resolveTypedef(ptrType.Type)); y != nil {
					s.fillRefs(y, false)
					x.size += y.size
					x.count += y.count
				}
			}
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
			}
			s.fillRefs(y, true)
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
			}
			s.fillRefs(y, true)
			s.record(y)
		}
	case *godwarf.FuncType:
		closureAddr, _ := readUintRaw(s.mem, x.Addr, int64(s.bi.Arch.PtrSize()))
		if closureAddr != 0 {
			funcAddr, _ := readUintRaw(s.mem, closureAddr, int64(s.bi.Arch.PtrSize()))
			if funcAddr != 0 {
				fn := s.bi.PCToFunc(funcAddr)
				if fn == nil {
					// 要查找堆对象
					return
				}
				cst := fn.closureStructType(s.bi)
				if closure := s.findObject(Address(closureAddr), cst); closure != nil {
					s.fillRefs(closure, false)
					x.size += closure.size
					x.count += closure.count
				}
			}
		}
	default:
	}
}

var atomicPointerRegex = regexp.MustCompile(`^sync/atomic.Pointer\[.*\]$`)

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

	heapScope := &HeapScope{mem: t.Memory(), bi: t.BinInfo()}
	err = heapScope.readHeap(scope)
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
						}
						ors.HeapScope.mem = &FallbackMemory{l.mem}
						ors.fillRefs(root, true)
						ors.record(root)
					}
				}
			}
		}
	}

	ors.mem = t.Memory()
	ors.gr = nil
	ors.stackVisited = nil
	pvs, _ := scope.PackageVariables(loadSingleValue)
	for _, pv := range pvs {
		if pv.Addr != 0 {
			//if strings.Contains(pv.Name, "pollmanager") {
			//	continue
			//}
			root := &ReferenceVariable{
				Addr:     pv.Addr,
				Name:     pv.Name,
				RealType: pv.RealType,
			}
			ors.fillRefs(root, true)
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
				}
				ors.fillRefs(root, true)
				ors.record(root)
			}
		}
	}

	ors.pb.flush()
	return nil
}
