package api

import (
	"debug/gosym"
	"strconv"

	"github.com/derekparker/delve/proc"
)

// convertBreakpoint converts an internal breakpoint to an API Breakpoint.
func ConvertBreakpoint(bp *proc.Breakpoint) *Breakpoint {
	b := &Breakpoint{
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

	return b
}

// convertThread converts an internal thread to an API Thread.
func ConvertThread(th *proc.Thread) *Thread {
	var (
		function *Function
		file     string
		line     int
		pc       uint64
	)

	loc, err := th.Location()
	if err == nil {
		pc = loc.PC
		file = loc.File
		line = loc.Line
		function = ConvertFunction(loc.Fn)
	}

	return &Thread{
		ID:       th.Id,
		PC:       pc,
		File:     file,
		Line:     line,
		Function: function,
	}
}

// convertVar converts an internal variable to an API Variable.
func ConvertVar(v *proc.Variable) Variable {
	return Variable{
		Name:  v.Name,
		Value: v.Value,
		Type:  v.Type,
	}
}

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

// convertGoroutine converts an internal Goroutine to an API Goroutine.
func ConvertGoroutine(g *proc.G) *Goroutine {
	return &Goroutine{
		ID:          g.Id,
		Current:     ConvertLocation(g.Current),
		UserCurrent: ConvertLocation(g.UserCurrent()),
		Go:          ConvertLocation(g.Go()),
	}
}

func ConvertLocation(loc proc.Location) Location {
	return Location{
		PC:       loc.PC,
		File:     loc.File,
		Line:     loc.Line,
		Function: ConvertFunction(loc.Fn),
	}
}
