package api

import (
	"bytes"
	"fmt"
	"go/constant"
	"go/printer"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc"
)

// ConvertBreakpoint converts from a proc.Breakpoint to
// an api.Breakpoint.
func ConvertBreakpoint(bp *proc.Breakpoint) *Breakpoint {
	b := &Breakpoint{
		Name:          bp.Name,
		ID:            bp.LogicalID,
		FunctionName:  bp.FunctionName,
		File:          bp.File,
		Line:          bp.Line,
		Addr:          bp.Addr,
		Tracepoint:    bp.Tracepoint,
		TraceReturn:   bp.TraceReturn,
		Stacktrace:    bp.Stacktrace,
		Goroutine:     bp.Goroutine,
		Variables:     bp.Variables,
		LoadArgs:      LoadConfigFromProc(bp.LoadArgs),
		LoadLocals:    LoadConfigFromProc(bp.LoadLocals),
		TotalHitCount: bp.TotalHitCount,
		Addrs:         []uint64{bp.Addr},
	}

	b.HitCount = map[string]uint64{}
	for idx := range bp.HitCount {
		b.HitCount[strconv.Itoa(idx)] = bp.HitCount[idx]
	}

	var buf bytes.Buffer
	printer.Fprint(&buf, token.NewFileSet(), bp.Cond)
	b.Cond = buf.String()

	return b
}

// ConvertBreakpoints converts a slice of physical breakpoints into a slice
// of logical breakpoints.
// The input must be sorted by increasing LogicalID
func ConvertBreakpoints(bps []*proc.Breakpoint) []*Breakpoint {
	if len(bps) <= 0 {
		return nil
	}
	r := make([]*Breakpoint, 0, len(bps))
	for _, bp := range bps {
		if len(r) > 0 {
			if r[len(r)-1].ID == bp.LogicalID {
				r[len(r)-1].Addrs = append(r[len(r)-1].Addrs, bp.Addr)
				continue
			} else if r[len(r)-1].ID > bp.LogicalID {
				panic("input not sorted")
			}
		}
		r = append(r, ConvertBreakpoint(bp))
	}
	return r
}

// ConvertThread converts a proc.Thread into an
// api thread.
func ConvertThread(th proc.Thread) *Thread {
	var (
		function *Function
		file     string
		line     int
		pc       uint64
		gid      int
	)

	loc, err := th.Location()
	if err == nil {
		pc = loc.PC
		file = loc.File
		line = loc.Line
		function = ConvertFunction(loc.Fn)
	}

	var bp *Breakpoint

	if b := th.Breakpoint(); b.Active {
		bp = ConvertBreakpoint(b.Breakpoint)
	}

	if g, _ := proc.GetG(th); g != nil {
		gid = g.ID
	}

	return &Thread{
		ID:          th.ThreadID(),
		PC:          pc,
		File:        file,
		Line:        line,
		Function:    function,
		GoroutineID: gid,
		Breakpoint:  bp,
	}
}

// ConvertThreads converts a slice of proc.Thread into a slice of api.Thread.
func ConvertThreads(threads []proc.Thread) []*Thread {
	r := make([]*Thread, len(threads))
	for i := range threads {
		r[i] = ConvertThread(threads[i])
	}
	return r
}

func PrettyTypeName(typ godwarf.Type) string {
	if typ == nil {
		return ""
	}
	if typ.Common().Name != "" {
		return typ.Common().Name
	}
	r := typ.String()
	if r == "*void" {
		return "unsafe.Pointer"
	}
	return r
}

func convertFloatValue(v *proc.Variable, sz int) string {
	switch v.FloatSpecial {
	case proc.FloatIsPosInf:
		return "+Inf"
	case proc.FloatIsNegInf:
		return "-Inf"
	case proc.FloatIsNaN:
		return "NaN"
	}
	f, _ := constant.Float64Val(v.Value)
	return strconv.FormatFloat(f, 'f', -1, sz)
}

// ConvertVar converts from proc.Variable to api.Variable.
func ConvertVar(v *proc.Variable) *Variable {
	r := Variable{
		Addr:     v.Addr,
		OnlyAddr: v.OnlyAddr,
		Name:     v.Name,
		Kind:     v.Kind,
		Len:      v.Len,
		Cap:      v.Cap,
		Flags:    VariableFlags(v.Flags),
		Base:     v.Base,

		LocationExpr: v.LocationExpr.String(),
		DeclLine:     v.DeclLine,
	}

	r.Type = PrettyTypeName(v.DwarfType)
	r.RealType = PrettyTypeName(v.RealType)

	if v.Unreadable != nil {
		r.Unreadable = v.Unreadable.Error()
	}

	r.Value = VariableValueAsString(v)

	switch v.Kind {
	case reflect.Complex64:
		r.Children = make([]Variable, 2)
		r.Len = 2

		r.Children[0].Name = "real"
		r.Children[0].Kind = reflect.Float32

		r.Children[1].Name = "imaginary"
		r.Children[1].Kind = reflect.Float32

		if v.Value != nil {
			real, _ := constant.Float64Val(constant.Real(v.Value))
			r.Children[0].Value = strconv.FormatFloat(real, 'f', -1, 32)

			imag, _ := constant.Float64Val(constant.Imag(v.Value))
			r.Children[1].Value = strconv.FormatFloat(imag, 'f', -1, 32)
		} else {
			r.Children[0].Value = "nil"
			r.Children[1].Value = "nil"
		}

	case reflect.Complex128:
		r.Children = make([]Variable, 2)
		r.Len = 2

		r.Children[0].Name = "real"
		r.Children[0].Kind = reflect.Float64

		r.Children[1].Name = "imaginary"
		r.Children[1].Kind = reflect.Float64

		if v.Value != nil {
			real, _ := constant.Float64Val(constant.Real(v.Value))
			r.Children[0].Value = strconv.FormatFloat(real, 'f', -1, 64)

			imag, _ := constant.Float64Val(constant.Imag(v.Value))
			r.Children[1].Value = strconv.FormatFloat(imag, 'f', -1, 64)
		} else {
			r.Children[0].Value = "nil"
			r.Children[1].Value = "nil"
		}

	default:
		r.Children = make([]Variable, len(v.Children))

		for i := range v.Children {
			r.Children[i] = *ConvertVar(&v.Children[i])
		}
	}

	return &r
}

func VariableValueAsString(v *proc.Variable) string {
	if v.Value == nil {
		return ""
	}
	switch v.Kind {
	case reflect.Float32:
		return convertFloatValue(v, 32)
	case reflect.Float64:
		return convertFloatValue(v, 64)
	case reflect.String, reflect.Func:
		return constant.StringVal(v.Value)
	default:
		if cd := v.ConstDescr(); cd != "" {
			return fmt.Sprintf("%s (%s)", cd, v.Value.String())
		} else {
			return v.Value.String()
		}
	}
}

// ConvertVars converts from []*proc.Variable to []api.Variable.
func ConvertVars(pv []*proc.Variable) []Variable {
	if pv == nil {
		return nil
	}
	vars := make([]Variable, 0, len(pv))
	for _, v := range pv {
		vars = append(vars, *ConvertVar(v))
	}
	return vars
}

// ConvertFunction converts from gosym.Func to
// api.Function.
func ConvertFunction(fn *proc.Function) *Function {
	if fn == nil {
		return nil
	}

	// fn here used to be a *gosym.Func, the fields Type and GoType below
	// corresponded to the homonymous field of gosym.Func. Since the contents of
	// those fields is not documented their value was replaced with 0 when
	// gosym.Func was replaced by debug_info entries.
	return &Function{
		Name_:     fn.Name,
		Type:      0,
		Value:     fn.Entry,
		GoType:    0,
		Optimized: fn.Optimized(),
	}
}

// ConvertGoroutine converts from proc.G to api.Goroutine.
func ConvertGoroutine(g *proc.G) *Goroutine {
	th := g.Thread
	tid := 0
	if th != nil {
		tid = th.ThreadID()
	}
	if g.Unreadable != nil {
		return &Goroutine{Unreadable: g.Unreadable.Error()}
	}
	return &Goroutine{
		ID:             g.ID,
		CurrentLoc:     ConvertLocation(g.CurrentLoc),
		UserCurrentLoc: ConvertLocation(g.UserCurrent()),
		GoStatementLoc: ConvertLocation(g.Go()),
		StartLoc:       ConvertLocation(g.StartLoc()),
		ThreadID:       tid,
		Labels:         g.Labels(),
	}
}

// ConvertGoroutines converts from []*proc.G to []*api.Goroutine.
func ConvertGoroutines(gs []*proc.G) []*Goroutine {
	goroutines := make([]*Goroutine, len(gs))
	for i := range gs {
		goroutines[i] = ConvertGoroutine(gs[i])
	}
	return goroutines
}

// ConvertLocation converts from proc.Location to api.Location.
func ConvertLocation(loc proc.Location) Location {
	return Location{
		PC:       loc.PC,
		File:     loc.File,
		Line:     loc.Line,
		Function: ConvertFunction(loc.Fn),
	}
}

// ConvertAsmInstruction converts from proc.AsmInstruction to api.AsmInstruction.
func ConvertAsmInstruction(inst proc.AsmInstruction, text string) AsmInstruction {
	var destloc *Location
	if inst.DestLoc != nil {
		r := ConvertLocation(*inst.DestLoc)
		destloc = &r
	}
	return AsmInstruction{
		Loc:        ConvertLocation(inst.Loc),
		DestLoc:    destloc,
		Text:       text,
		Bytes:      inst.Bytes,
		Breakpoint: inst.Breakpoint,
		AtPC:       inst.AtPC,
	}
}

// LoadConfigToProc converts an api.LoadConfig to proc.LoadConfig.
func LoadConfigToProc(cfg *LoadConfig) *proc.LoadConfig {
	if cfg == nil {
		return nil
	}
	return &proc.LoadConfig{
		FollowPointers:     cfg.FollowPointers,
		MaxVariableRecurse: cfg.MaxVariableRecurse,
		MaxStringLen:       cfg.MaxStringLen,
		MaxArrayValues:     cfg.MaxArrayValues,
		MaxStructFields:    cfg.MaxStructFields,
		MaxMapBuckets:      0, // MaxMapBuckets is set internally by pkg/proc, read its documentation for an explanation.
	}
}

// LoadConfigFromProc converts a proc.LoadConfig to api.LoadConfig.
func LoadConfigFromProc(cfg *proc.LoadConfig) *LoadConfig {
	if cfg == nil {
		return nil
	}
	return &LoadConfig{
		FollowPointers:     cfg.FollowPointers,
		MaxVariableRecurse: cfg.MaxVariableRecurse,
		MaxStringLen:       cfg.MaxStringLen,
		MaxArrayValues:     cfg.MaxArrayValues,
		MaxStructFields:    cfg.MaxStructFields,
	}
}

var canonicalRegisterOrder = map[string]int{
	// amd64
	"rip": 0,
	"rsp": 1,
	"rax": 2,
	"rbx": 3,
	"rcx": 4,
	"rdx": 5,

	// arm64
	"pc": 0,
	"sp": 1,
}

// ConvertRegisters converts proc.Register to api.Register for a slice.
func ConvertRegisters(in *op.DwarfRegisters, dwarfRegisterToString func(int, *op.DwarfRegister) (string, bool, string), floatingPoint bool) (out []Register) {
	out = make([]Register, 0, in.CurrentSize())
	for i := 0; i < in.CurrentSize(); i++ {
		reg := in.Reg(uint64(i))
		if reg == nil {
			continue
		}
		name, fp, repr := dwarfRegisterToString(i, reg)
		if !floatingPoint && fp {
			continue
		}
		out = append(out, Register{name, repr, i})
	}
	// Sort the registers in a canonical order we prefer, this is mostly
	// because the DWARF register numbering for AMD64 is weird.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		an, aok := canonicalRegisterOrder[strings.ToLower(a.Name)]
		bn, bok := canonicalRegisterOrder[strings.ToLower(b.Name)]
		// Registers that don't appear in canonicalRegisterOrder sort after registers that do.
		if !aok {
			an = 1000
		}
		if !bok {
			bn = 1000
		}
		if an == bn {
			// keep registers that don't appear in canonicalRegisterOrder in DWARF order
			return a.DwarfNumber < b.DwarfNumber
		}
		return an < bn

	})
	return
}

func ConvertImage(image *proc.Image) Image {
	return Image{Path: image.Path, Address: image.StaticBase}
}
