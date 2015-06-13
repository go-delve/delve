package api

import "github.com/derekparker/delve/proc"

// convertBreakpoint converts an internal breakpoint to an API Breakpoint.
func ConvertBreakpoint(bp *proc.Breakpoint) *Breakpoint {
	return &Breakpoint{
		ID:           bp.ID,
		FunctionName: bp.FunctionName,
		File:         bp.File,
		Line:         bp.Line,
		Addr:         bp.Addr,
	}
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
		if loc.Fn != nil {
			function = &Function{
				Name:   loc.Fn.Name,
				Type:   loc.Fn.Type,
				Value:  loc.Fn.Value,
				GoType: loc.Fn.GoType,
			}
		}
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

// convertGoroutine converts an internal Goroutine to an API Goroutine.
func ConvertGoroutine(g *proc.G) *Goroutine {
	var function *Function
	if g.Func != nil {
		function = &Function{
			Name:   g.Func.Name,
			Type:   g.Func.Type,
			Value:  g.Func.Value,
			GoType: g.Func.GoType,
		}
	}

	return &Goroutine{
		ID:       g.Id,
		PC:       g.PC,
		File:     g.File,
		Line:     g.Line,
		Function: function,
	}
}
