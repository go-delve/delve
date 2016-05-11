package dwarf

import (
	"debug/gosym"
	"reflect"
	"strings"
	"unsafe"

	"golang.org/x/debug/dwarf"

	"github.com/derekparker/delve/pkg/dwarf/frame"
	"github.com/derekparker/delve/pkg/dwarf/line"
	"github.com/derekparker/delve/pkg/dwarf/reader"
)

type Dwarf struct {
	Frame    frame.FrameDescriptionEntries
	Line     line.DebugLines
	Types    map[string]dwarf.Offset
	Packages map[string]string

	// We keep this here so we have all debug info
	// in a single place. Not technically dwarf.
	symboltab *gosym.Table

	data *dwarf.Data
}

func Parse(path string) (*Dwarf, error) {
	exe, err := newExecutable(path)
	if err != nil {
		return nil, err
	}
	d, err := parseDwarf(exe)
	if err != nil {
		return nil, err
	}
	line, err := parseLine(exe)
	if err != nil {
		return nil, err
	}
	frame, err := parseFrame(exe)
	if err != nil {
		return nil, err
	}
	syms, err := parseGoSymbols(exe)
	if err != nil {
		return nil, err
	}
	rdr := reader.New(d)
	dw := &Dwarf{
		Frame:     frame,
		Line:      line,
		data:      d,
		Types:     loadTypes(rdr),
		Packages:  loadPackages(rdr),
		symboltab: syms,
	}
	return dw, nil
}

func (d *Dwarf) Reader() *reader.Reader {
	return reader.New(d.data)
}

func (d *Dwarf) Type(off dwarf.Offset) (dwarf.Type, error) {
	return d.data.Type(off)
}

func (d *Dwarf) TypeNamed(name string) (dwarf.Type, error) {
	off, found := d.Types[name]
	if !found {
		return nil, reader.TypeNotFoundErr
	}
	return d.data.Type(off)
}

func (d *Dwarf) PCToLine(pc uint64) (string, int, *gosym.Func) {
	return d.symboltab.PCToLine(pc)
}

func (d *Dwarf) PCToFunc(pc uint64) *gosym.Func {
	return d.symboltab.PCToFunc(pc)
}

func (d *Dwarf) LineToPC(file string, line int) (uint64, *gosym.Func, error) {
	return d.symboltab.LineToPC(file, line)
}

func (d *Dwarf) Funcs() []gosym.Func {
	return d.symboltab.Funcs
}

func (d *Dwarf) LookupFunc(name string) *gosym.Func {
	return d.symboltab.LookupFunc(name)
}

func (d *Dwarf) Files() map[string]*gosym.Obj {
	return d.symboltab.Files
}

// Types returns list of types present in the debugged program.
func (d *Dwarf) TypeList() ([]string, error) {
	types := make([]string, 0, len(d.Types))
	for k := range d.Types {
		types = append(types, k)
	}
	return types, nil
}

func loadTypes(rdr *reader.Reader) map[string]dwarf.Offset {
	types := make(map[string]dwarf.Offset)
	rdr.Seek(0)
	for entry, err := rdr.NextType(); entry != nil; entry, err = rdr.NextType() {
		if err != nil {
			break
		}
		name, ok := entry.Val(dwarf.AttrName).(string)
		if !ok {
			continue
		}
		if _, exists := types[name]; !exists {
			types[name] = entry.Offset
		}
	}
	return types
}

func loadPackages(rdr *reader.Reader) map[string]string {
	rdr.Seek(0)
	packages := map[string]string{}
	for entry, err := rdr.Next(); entry != nil; entry, err = rdr.Next() {
		if err != nil {
			// TODO(derekparker) do not panic here
			panic(err)
		}

		if entry.Tag != dwarf.TagTypedef && entry.Tag != dwarf.TagBaseType && entry.Tag != dwarf.TagClassType && entry.Tag != dwarf.TagStructType {
			continue
		}

		typename, ok := entry.Val(dwarf.AttrName).(string)
		if !ok || complexType(typename) {
			continue
		}

		dot := strings.LastIndex(typename, ".")
		if dot < 0 {
			continue
		}
		path := typename[:dot]
		slash := strings.LastIndex(path, "/")
		if slash < 0 || slash+1 >= len(path) {
			continue
		}
		name := path[slash+1:]
		packages[name] = path
	}
	return packages
}

func PointerTo(typ dwarf.Type) dwarf.Type {
	return &dwarf.PtrType{dwarf.CommonType{int64(unsafe.Sizeof(uintptr(1))), "", reflect.Ptr, 0}, typ}
}

func complexType(typename string) bool {
	for _, ch := range typename {
		switch ch {
		case '*', '[', '<', '{', '(', ' ':
			return true
		}
	}
	return false
}
