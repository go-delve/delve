package godwarf

import (
	"debug/dwarf"
	"fmt"
	"sort"
)

// Entry represents a debug_info entry.
// When calling Val, if the entry does not have the specified attribute, the
// entry specified by DW_AT_abstract_origin will be searched recursively.
type Entry interface {
	Val(dwarf.Attr) interface{}
	AttrField(dwarf.Attr) *dwarf.Field
}

type compositeEntry []*dwarf.Entry

func (ce compositeEntry) Val(attr dwarf.Attr) interface{} {
	if f := ce.AttrField(attr); f != nil {
		return f.Val
	}
	return nil
}

func (ce compositeEntry) AttrField(a dwarf.Attr) *dwarf.Field {
	for _, e := range ce {
		if f := e.AttrField(a); f != nil {
			return f
		}
	}
	return nil
}

// LoadAbstractOriginAndSpecification loads the entry corresponding to the
// DW_AT_abstract_origin and/or DW_AT_specification of entry and returns a
// combination of entry and its abstract origin. If a DIE has both a
// specification and an abstract origin the specification will be ignored, the
// DWARF standard is unclear on how this should be handled
func LoadAbstractOriginAndSpecification(entry *dwarf.Entry, aordr *dwarf.Reader) (Entry, dwarf.Offset) {
	ao, ok := getAbstractOriginOrSpecification(entry)
	if !ok {
		return entry, entry.Offset
	}

	r := []*dwarf.Entry{entry}

	for {
		aordr.Seek(ao)
		e, _ := aordr.Next()
		if e == nil {
			break
		}
		r = append(r, e)

		ao, ok = getAbstractOriginOrSpecification(e)
		if !ok {
			break
		}
	}

	return compositeEntry(r), entry.Offset
}

func getAbstractOriginOrSpecification(e *dwarf.Entry) (dwarf.Offset, bool) {
	ao, ok := e.Val(dwarf.AttrAbstractOrigin).(dwarf.Offset)
	if ok {
		return ao, true
	}
	sp, ok := e.Val(dwarf.AttrSpecification).(dwarf.Offset)
	if ok {
		return sp, true
	}
	return dwarf.Offset(0), false
}

// Tree represents a tree of dwarf objects.
type Tree struct {
	Entry
	typ      Type
	Tag      dwarf.Tag
	Offset   dwarf.Offset
	Ranges   [][2]uint64
	Children []*Tree
}

// LoadTree returns the tree of DIE rooted at offset 'off'.
// Abstract origins are automatically loaded, if present.
// DIE ranges are bubbled up automatically, if the child of a DIE covers a
// range of addresses that is not covered by its parent LoadTree will fix
// the parent entry.
func LoadTree(off dwarf.Offset, dw *dwarf.Data, staticBase uint64) (*Tree, error) {
	rdr := dw.Reader()
	rdr.Seek(off)

	e, err := rdr.Next()
	if err != nil {
		return nil, err
	}
	r := entryToTreeInternal(e)
	r.Children, err = loadTreeChildren(e, rdr)
	if err != nil {
		return nil, err
	}

	err = r.resolveRanges(dw, staticBase)
	if err != nil {
		return nil, err
	}
	r.resolveAbstractAndSpecificationEntries(rdr)

	return r, nil
}

// EntryToTree converts a single entry, without children, to a *Tree object.
func EntryToTree(entry *dwarf.Entry) *Tree {
	if entry.Children {
		panic(fmt.Sprintf("EntryToTree called on entry with children; "+
			"LoadTree should have been used instead. entry: %+v", entry))
	}
	return entryToTreeInternal(entry)
}

func entryToTreeInternal(entry *dwarf.Entry) *Tree {
	return &Tree{Entry: entry, Offset: entry.Offset, Tag: entry.Tag}
}

func loadTreeChildren(e *dwarf.Entry, rdr *dwarf.Reader) ([]*Tree, error) {
	if !e.Children {
		return nil, nil
	}
	children := []*Tree{}
	for {
		e, err := rdr.Next()
		if err != nil {
			return nil, err
		}
		if e.Tag == 0 {
			break
		}
		child := entryToTreeInternal(e)
		child.Children, err = loadTreeChildren(e, rdr)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return children, nil
}

func (n *Tree) resolveRanges(dw *dwarf.Data, staticBase uint64) error {
	var err error
	n.Ranges, err = dw.Ranges(n.Entry.(*dwarf.Entry))
	if err != nil {
		return err
	}
	for i := range n.Ranges {
		n.Ranges[i][0] += staticBase
		n.Ranges[i][1] += staticBase
	}
	n.Ranges = normalizeRanges(n.Ranges)

	for _, child := range n.Children {
		err := child.resolveRanges(dw, staticBase)
		if err != nil {
			return err
		}
		n.Ranges = fuseRanges(n.Ranges, child.Ranges)
	}
	return nil
}

// normalizeRanges sorts rngs by starting point and fuses overlapping entries.
func normalizeRanges(rngs [][2]uint64) [][2]uint64 {
	const (
		start = 0
		end   = 1
	)

	if len(rngs) == 0 {
		return rngs
	}

	sort.Slice(rngs, func(i, j int) bool {
		return rngs[i][start] <= rngs[j][start]
	})

	// eliminate invalid entries
	out := rngs[:0]
	for i := range rngs {
		if rngs[i][start] < rngs[i][end] {
			out = append(out, rngs[i])
		}
	}
	rngs = out

	// fuse overlapping entries
	out = rngs[:1]
	for i := 1; i < len(rngs); i++ {
		cur := rngs[i]
		if cur[start] <= out[len(out)-1][end] {
			out[len(out)-1][end] = max(cur[end], out[len(out)-1][end])
		} else {
			out = append(out, cur)
		}
	}
	return out
}

func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// fuseRanges fuses rngs2 into rngs1, it's the equivalent of
//
//	normalizeRanges(append(rngs1, rngs2))
//
// but more efficient.
func fuseRanges(rngs1, rngs2 [][2]uint64) [][2]uint64 {
	if rangesContains(rngs1, rngs2) {
		return rngs1
	}

	return normalizeRanges(append(rngs1, rngs2...))
}

// rangesContains checks that rngs1 is a superset of rngs2.
func rangesContains(rngs1, rngs2 [][2]uint64) bool {
	i, j := 0, 0
	for {
		if i >= len(rngs1) {
			return false
		}
		if j >= len(rngs2) {
			return true
		}
		if rangeContains(rngs1[i], rngs2[j]) {
			j++
		} else {
			i++
		}
	}
}

// rangeContains checks that a contains b.
func rangeContains(a, b [2]uint64) bool {
	return a[0] <= b[0] && a[1] >= b[1]
}

func (n *Tree) resolveAbstractAndSpecificationEntries(rdr *dwarf.Reader) {
	n.Entry, n.Offset = LoadAbstractOriginAndSpecification(n.Entry.(*dwarf.Entry), rdr)
	for _, child := range n.Children {
		child.resolveAbstractAndSpecificationEntries(rdr)
	}
}

// ContainsPC returns true if the ranges of this DIE contains PC.
func (n *Tree) ContainsPC(pc uint64) bool {
	for _, rng := range n.Ranges {
		if rng[0] > pc {
			return false
		}
		if rng[0] <= pc && pc < rng[1] {
			return true
		}
	}
	return false
}

func (n *Tree) Type(dw *dwarf.Data, index int, typeCache map[dwarf.Offset]Type) (Type, error) {
	if n.typ == nil {
		offset, ok := n.Val(dwarf.AttrType).(dwarf.Offset)
		if !ok {
			return nil, fmt.Errorf("malformed variable DIE (offset)")
		}

		var err error
		n.typ, err = ReadType(dw, index, offset, typeCache)
		if err != nil {
			return nil, err
		}
	}
	return n.typ, nil
}
