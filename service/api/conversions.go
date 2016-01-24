package api

import (
	"bytes"
	"debug/gosym"
	"go/constant"
	"go/printer"
	"go/token"
	"golang.org/x/debug/dwarf"
	"reflect"
	"strconv"

	"github.com/derekparker/delve/proc"
)

// ConvertBreakpoint converts from a proc.Breakpoint to
// an api.Breakpoint.
func ConvertBreakpoint(bp *proc.Breakpoint) *Breakpoint {
	b := &Breakpoint{
		Name:          bp.Name,
		ID:            bp.ID,
		FunctionName:  bp.FunctionName,
		File:          bp.File,
		Line:          bp.Line,
		Addr:          bp.Addr,
		Tracepoint:    bp.Tracepoint,
		Stacktrace:    bp.Stacktrace,
		Goroutine:     bp.Goroutine,
		Variables:     bp.Variables,
		TotalHitCount: bp.TotalHitCount,
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

// ConvertThread converts a proc.Thread into an
// api thread.
func ConvertThread(th *proc.Thread) *Thread {
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

	if th.CurrentBreakpoint != nil && th.BreakpointConditionMet {
		bp = ConvertBreakpoint(th.CurrentBreakpoint)
	}

	if g, _ := th.GetG(); g != nil {
		gid = g.ID
	}

	return &Thread{
		ID:          th.ID,
		PC:          pc,
		File:        file,
		Line:        line,
		Function:    function,
		GoroutineID: gid,
		Breakpoint:  bp,
	}
}

func prettyTypeName(typ dwarf.Type) string {
	if typ == nil {
		return ""
	}
	r := typ.String()
	if r == "*void" {
		return "unsafe.Pointer"
	}
	return r
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
	}

	r.Type = prettyTypeName(v.DwarfType)
	r.RealType = prettyTypeName(v.RealType)

	if v.Unreadable != nil {
		r.Unreadable = v.Unreadable.Error()
	}

	if v.Value != nil {
		switch v.Kind {
		case reflect.Float32:
			f, _ := constant.Float64Val(v.Value)
			r.Value = strconv.FormatFloat(f, 'f', -1, 32)
		case reflect.Float64:
			f, _ := constant.Float64Val(v.Value)
			r.Value = strconv.FormatFloat(f, 'f', -1, 64)
		case reflect.String, reflect.Func:
			r.Value = constant.StringVal(v.Value)
		default:
			r.Value = v.Value.String()
		}
	}

	switch v.Kind {
	case reflect.Complex64:
		r.Children = make([]Variable, 2)
		r.Len = 2

		real, _ := constant.Float64Val(constant.Real(v.Value))
		imag, _ := constant.Float64Val(constant.Imag(v.Value))

		r.Children[0].Name = "real"
		r.Children[0].Kind = reflect.Float32
		r.Children[0].Value = strconv.FormatFloat(real, 'f', -1, 32)

		r.Children[1].Name = "imaginary"
		r.Children[1].Kind = reflect.Float32
		r.Children[1].Value = strconv.FormatFloat(imag, 'f', -1, 32)
	case reflect.Complex128:
		r.Children = make([]Variable, 2)
		r.Len = 2

		real, _ := constant.Float64Val(constant.Real(v.Value))
		imag, _ := constant.Float64Val(constant.Imag(v.Value))

		r.Children[0].Name = "real"
		r.Children[0].Kind = reflect.Float64
		r.Children[0].Value = strconv.FormatFloat(real, 'f', -1, 64)

		r.Children[1].Name = "imaginary"
		r.Children[1].Kind = reflect.Float64
		r.Children[1].Value = strconv.FormatFloat(imag, 'f', -1, 64)

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
func ConvertFunction(fn *gosym.Func) *Function {
	if fn == nil {
		return nil
	}

	return &Function{
		Name:   fn.Name,
		Type:   fn.Type,
		Value:  fn.Value,
		GoType: fn.GoType,
	}
}

// ConvertGoroutine converts from proc.G to api.Goroutine.
func ConvertGoroutine(g *proc.G) *Goroutine {
	return &Goroutine{
		ID:             g.ID,
		CurrentLoc:     ConvertLocation(g.CurrentLoc),
		UserCurrentLoc: ConvertLocation(g.UserCurrent()),
		GoStatementLoc: ConvertLocation(g.Go()),
	}
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
