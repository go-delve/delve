package api

import (
	"bytes"
	"fmt"
	"go/constant"
	"go/printer"
	"go/token"
	"reflect"
	"strconv"

	"github.com/go-delve/delve/pkg/debug"
	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/proc"
)

// ConvertBreakpoint converts from a debug.Breakpoint to
// an api.Breakpoint.
func ConvertBreakpoint(bp *debug.Breakpoint) *Breakpoint {
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
func ConvertBreakpoints(bps []*debug.Breakpoint) []*Breakpoint {
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
func ConvertThread(th proc.Thread, bi *debug.BinaryInfo, b debug.BreakpointState) *Thread {
	var (
		function *Function
		file     string
		line     int
		pc       uint64
		gid      int
	)

	pc, err := th.PC()
	loc := bi.PCToLocation(pc)
	if err == nil {
		pc = loc.PC
		file = loc.File
		line = loc.Line
		function = ConvertFunction(loc.Fn)
	}

	var bp *Breakpoint

	if b.Active {
		bp = ConvertBreakpoint(b.Breakpoint)
	}

	if g, _ := debug.GetG(th, bi); g != nil {
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

func prettyTypeName(typ godwarf.Type) string {
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

func convertFloatValue(v *debug.Variable, sz int) string {
	switch v.FloatSpecial {
	case debug.FloatIsPosInf:
		return "+Inf"
	case debug.FloatIsNegInf:
		return "-Inf"
	case debug.FloatIsNaN:
		return "NaN"
	}
	f, _ := constant.Float64Val(v.Value)
	return strconv.FormatFloat(f, 'f', -1, sz)
}

// ConvertVar converts from proc.Variable to api.Variable.
func ConvertVar(v *debug.Variable) *Variable {
	r := Variable{
		Addr:     v.Addr,
		OnlyAddr: v.OnlyAddr,
		Name:     v.Name,
		Kind:     v.Kind,
		Len:      v.Len,
		Cap:      v.Cap,
		Flags:    VariableFlags(v.Flags),
		Base:     v.Base,

		LocationExpr: v.LocationExpr,
		DeclLine:     v.DeclLine,
	}

	r.Type = prettyTypeName(v.DwarfType)
	r.RealType = prettyTypeName(v.RealType)

	if v.Unreadable != nil {
		r.Unreadable = v.Unreadable.Error()
	}

	if v.Value != nil {
		switch v.Kind {
		case reflect.Float32:
			r.Value = convertFloatValue(v, 32)
		case reflect.Float64:
			r.Value = convertFloatValue(v, 64)
		case reflect.String, reflect.Func:
			r.Value = constant.StringVal(v.Value)
		default:
			if cd := v.ConstDescr(); cd != "" {
				r.Value = fmt.Sprintf("%s (%s)", cd, v.Value.String())
			} else {
				r.Value = v.Value.String()
			}
		}
	}

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

// ConvertFunction converts from gosym.Func to
// api.Function.
func ConvertFunction(fn *debug.Function) *Function {
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
func ConvertGoroutine(g *debug.G) *Goroutine {
	th := g.Thread
	tid := 0
	if th != nil {
		tid = th.ThreadID()
	}
	r := &Goroutine{
		ID:             g.ID,
		CurrentLoc:     ConvertLocation(g.CurrentLoc),
		UserCurrentLoc: ConvertLocation(g.UserCurrent()),
		GoStatementLoc: ConvertLocation(g.Go()),
		StartLoc:       ConvertLocation(g.StartLoc()),
		ThreadID:       tid,
	}
	if g.Unreadable != nil {
		r.Unreadable = g.Unreadable.Error()
	}
	return r
}

// ConvertLocation converts from proc.Location to api.Location.
func ConvertLocation(loc debug.Location) Location {
	return Location{
		PC:       loc.PC,
		File:     loc.File,
		Line:     loc.Line,
		Function: ConvertFunction(loc.Fn),
	}
}

// ConvertAsmInstruction converts from proc.AsmInstruction to api.AsmInstruction.
func ConvertAsmInstruction(inst debug.AsmInstruction, text string) AsmInstruction {
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
func LoadConfigToProc(cfg *LoadConfig) *debug.LoadConfig {
	if cfg == nil {
		return nil
	}
	return &debug.LoadConfig{
		cfg.FollowPointers,
		cfg.MaxVariableRecurse,
		cfg.MaxStringLen,
		cfg.MaxArrayValues,
		cfg.MaxStructFields,
		0, // MaxMapBuckets is set internally by pkg/proc, read its documentation for an explanation.
	}
}

// LoadConfigFromProc converts a proc.LoadConfig to api.LoadConfig.
func LoadConfigFromProc(cfg *debug.LoadConfig) *LoadConfig {
	if cfg == nil {
		return nil
	}
	return &LoadConfig{
		cfg.FollowPointers,
		cfg.MaxVariableRecurse,
		cfg.MaxStringLen,
		cfg.MaxArrayValues,
		cfg.MaxStructFields,
	}
}

// ConvertRegisters converts proc.Register to api.Register for a slice.
func ConvertRegisters(in []proc.Register) (out []Register) {
	out = make([]Register, len(in))
	for i := range in {
		out[i] = Register{in[i].Name, in[i].Value}
	}
	return
}

// ConvertCheckpoint converts proc.Chekcpoint to api.Checkpoint.
func ConvertCheckpoint(in proc.Checkpoint) (out Checkpoint) {
	return Checkpoint(in)
}

func ConvertImage(image *debug.Image) Image {
	return Image{Path: image.Path, Address: image.StaticBase}
}
