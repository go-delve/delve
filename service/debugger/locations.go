package debugger

import (
	"debug/gosym"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/derekparker/delve/service/api"
)

const maxFindLocationCandidates = 5

type LocationSpec interface {
	Find(d *Debugger, locStr string) ([]api.Location, error)
}

type NormalLocationSpec struct {
	Base       string
	FuncBase   *FuncLocationSpec
	LineOffset int
}

type RegexLocationSpec struct {
	FuncRegex string
}

type AddrLocationSpec struct {
	Addr uint64
}

type OffsetLocationSpec struct {
	Offset int
}

type LineLocationSpec struct {
	Line int
}

type FuncLocationSpec struct {
	PackageName           string
	ReceiverName          string
	PackageOrReceiverName string
	BaseName              string
}

func parseLocationSpec(locStr string) (LocationSpec, error) {
	rest := locStr

	malformed := func(reason string) error {
		return fmt.Errorf("Malformed breakpoint location \"%s\" at %d: %s", locStr, len(locStr)-len(rest), reason)
	}

	if len(rest) <= 0 {
		return nil, malformed("empty string")
	}

	switch rest[0] {
	case '+', '-':
		offset, err := strconv.Atoi(rest)
		if err != nil {
			return nil, malformed(err.Error())
		}
		return &OffsetLocationSpec{offset}, nil

	case '/':
		rx, rest := readRegex(rest[1:])
		if len(rest) < 0 {
			return nil, malformed("non-terminated regular expression")
		}
		if len(rest) > 1 {
			return nil, malformed("no line offset can be specified for regular expression locations")
		}
		return &RegexLocationSpec{rx}, nil

	case '*':
		rest = rest[1:]
		addr, err := strconv.ParseInt(rest, 0, 64)
		if err != nil {
			return nil, malformed(err.Error())
		}
		if addr == 0 {
			return nil, malformed("can not set breakpoint at address 0x0")
		}
		return &AddrLocationSpec{uint64(addr)}, nil

	default:
		v := strings.SplitN(rest, ":", 2)

		if len(v) == 1 {
			n, err := strconv.ParseInt(v[0], 0, 64)
			if err == nil {
				return &LineLocationSpec{int(n)}, nil
			}
		}

		spec := &NormalLocationSpec{}

		spec.Base = v[0]
		spec.FuncBase = parseFuncLocationSpec(spec.Base)

		if len(v) < 2 {
			spec.LineOffset = -1
			return spec, nil
		}

		rest = v[1]

		var err error
		spec.LineOffset, err = strconv.Atoi(rest)
		if err != nil || spec.LineOffset < 0 {
			return nil, malformed("line offset negative or not a number")
		}

		return spec, nil
	}
}

func readRegex(in string) (rx string, rest string) {
	out := make([]rune, 0, len(in))
	escaped := false
	for i, ch := range in {
		if escaped {
			if ch == '/' {
				out = append(out, '/')
			} else {
				out = append(out, '\\')
				out = append(out, ch)
			}
			escaped = false
		} else {
			switch ch {
			case '\\':
				escaped = true
			case '/':
				return string(out), in[i:]
			default:
				out = append(out, ch)
			}
		}
	}
	return string(out), ""
}

func parseFuncLocationSpec(in string) *FuncLocationSpec {
	if strings.Index(in, "/") >= 0 {
		return nil
	}

	v := strings.Split(in, ".")
	var spec FuncLocationSpec
	switch len(v) {
	case 1:
		spec.BaseName = v[0]

	case 2:
		spec.BaseName = v[1]
		r := stripReceiverDecoration(v[0])
		if r != v[0] {
			spec.ReceiverName = r
		} else {
			spec.PackageOrReceiverName = r
		}

	case 3:
		spec.BaseName = v[2]
		spec.ReceiverName = stripReceiverDecoration(v[1])
		spec.PackageName = stripReceiverDecoration(v[0])

	default:
		return nil
	}

	return &spec
}

func stripReceiverDecoration(in string) string {
	if len(in) < 3 {
		return in
	}
	if (in[0] != '(') || (in[1] != '*') || (in[len(in)-1] != ')') {
		return in
	}

	return in[2 : len(in)-1]
}

func (spec *FuncLocationSpec) Match(sym *gosym.Sym) bool {
	if spec.BaseName != sym.BaseName() {
		return false
	}

	recv := stripReceiverDecoration(sym.ReceiverName())
	if spec.ReceiverName != "" && spec.ReceiverName != recv {
		return false
	}
	if spec.PackageName != "" && spec.PackageName != sym.PackageName() {
		return false
	}
	if spec.PackageOrReceiverName != "" && spec.PackageOrReceiverName != sym.PackageName() && spec.PackageOrReceiverName != recv {
		return false
	}
	return true
}

func (loc *RegexLocationSpec) Find(d *Debugger, locStr string) ([]api.Location, error) {
	funcs := d.process.Funcs()
	matches, err := regexFilterFuncs(loc.FuncRegex, funcs)
	if err != nil {
		return nil, err
	}
	r := make([]api.Location, 0, len(matches))
	for i := range matches {
		addr, err := d.process.FindFunctionLocation(matches[i], true, 0)
		if err == nil {
			r = append(r, api.Location{PC: addr})
		}
	}
	return r, nil
}

func (loc *AddrLocationSpec) Find(d *Debugger, locStr string) ([]api.Location, error) {
	return []api.Location{{PC: loc.Addr}}, nil
}

func (loc *NormalLocationSpec) FileMatch(path string) bool {
	return strings.HasSuffix(path, loc.Base) && (path[len(path)-len(loc.Base)-1] == filepath.Separator)
}

func (loc *NormalLocationSpec) Find(d *Debugger, locStr string) ([]api.Location, error) {
	funcs := d.process.Funcs()
	files := d.process.Sources()

	candidates := []string{}
	for file := range files {
		if loc.FileMatch(file) {
			candidates = append(candidates, file)
			if len(candidates) >= maxFindLocationCandidates {
				break
			}
		}
	}

	if loc.FuncBase != nil {
		for _, f := range funcs {
			if f.Sym == nil {
				continue
			}
			if loc.FuncBase.Match(f.Sym) {
				candidates = append(candidates, f.Name)
				if len(candidates) >= maxFindLocationCandidates {
					break
				}
			}
		}
	}

	switch len(candidates) {
	case 1:
		var addr uint64
		var err error
		if candidates[0][0] == '/' {
			if loc.LineOffset < 0 {
				return nil, fmt.Errorf("Malformed breakpoint location, no line offset specified")
			}
			addr, err = d.process.FindFileLocation(candidates[0], loc.LineOffset)
		} else {
			if loc.LineOffset < 0 {
				addr, err = d.process.FindFunctionLocation(candidates[0], true, 0)
			} else {
				addr, err = d.process.FindFunctionLocation(candidates[0], false, loc.LineOffset)
			}
		}
		if err != nil {
			return nil, err
		}
		return []api.Location{{PC: addr}}, nil

	case 0:
		return nil, fmt.Errorf("Location \"%s\" not found", locStr)
	default:
		return nil, fmt.Errorf("Location \"%s\" ambiguous: %s…", locStr, strings.Join(candidates, ", "))
	}
}

func (loc *OffsetLocationSpec) Find(d *Debugger, locStr string) ([]api.Location, error) {
	cur, err := d.process.CurrentThread.Location()
	if err != nil {
		return nil, err
	}
	addr, err := d.process.FindFileLocation(cur.File, cur.Line+loc.Offset)
	return []api.Location{{PC: addr}}, err
}

func (loc *LineLocationSpec) Find(d *Debugger, locStr string) ([]api.Location, error) {
	cur, err := d.process.CurrentThread.Location()
	if err != nil {
		return nil, err
	}
	addr, err := d.process.FindFileLocation(cur.File, loc.Line)
	return []api.Location{{PC: addr}}, err
}
