package api

import (
	"debug/gosym"
	"os"
	"strings"

	"github.com/derekparker/delve/proc"
)

// convertBreakpoint converts an internal breakpoint to an API Breakpoint.
func ConvertBreakpoint(bp *proc.Breakpoint) *Breakpoint {
	return &Breakpoint{
		ID:           bp.ID,
		FunctionName: bp.FunctionName,
		File:         shortenFilePath(bp.File),
		Line:         bp.Line,
		Addr:         bp.Addr,
		Tracepoint:   bp.Tracepoint,
		Stacktrace:   bp.Stacktrace,
		Goroutine:    bp.Goroutine,
		Variables:    bp.Variables,
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
		function = ConvertFunction(loc.Fn)
	}

	return &Thread{
		ID:       th.Id,
		PC:       pc,
		File:     shortenFilePath(file),
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
		ID:       g.Id,
		PC:       g.PC,
		File:     shortenFilePath(g.File),
		Line:     g.Line,
		Function: ConvertFunction(g.Func),
	}
}

func ConvertLocation(loc proc.Location) Location {
	return Location{
		PC:       loc.PC,
		File:     shortenFilePath(loc.File),
		Line:     loc.Line,
		Function: ConvertFunction(loc.Fn),
	}
}

func shortenFilePath(fullPath string) string {
	workingDir, _ := os.Getwd()
	return strings.Replace(fullPath, workingDir, ".", 1)
}
