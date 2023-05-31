package reader

import (
	"debug/dwarf"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
)

type Variable struct {
	*godwarf.Tree
	// Depth represents the depth of the lexical block in which this variable
	// was declared, relative to a root scope (e.g. a function) passed to
	// Variables(). The depth is used to figure out if a variable is shadowed at
	// a particular pc by another one with the same name declared in an inner
	// block.
	Depth int
}

// VariablesFlags specifies some configuration flags for the Variables function.
type VariablesFlags uint8

const (
	VariablesOnlyVisible VariablesFlags = 1 << iota
	VariablesSkipInlinedSubroutines
	VariablesTrustDeclLine
	VariablesNoDeclLineCheck
)

// Variables returns a list of variables contained inside 'root'.
//
// If the VariablesOnlyVisible flag is set, only variables visible at 'pc' will be
// returned. If the VariablesSkipInlinedSubroutines is set, variables from
// inlined subroutines will be skipped.
func Variables(root *godwarf.Tree, pc uint64, line int, flags VariablesFlags) []Variable {
	return variablesInternal(nil, root, 0, pc, line, flags)
}

// variablesInternal appends to 'v' variables from 'root'. The function calls
// itself with an incremented scope for all sub-blocks in 'root'.
func variablesInternal(v []Variable, root *godwarf.Tree, depth int, pc uint64, line int, flags VariablesFlags) []Variable {
	switch root.Tag {
	case dwarf.TagInlinedSubroutine:
		if flags&VariablesSkipInlinedSubroutines != 0 {
			return v
		}
		fallthrough
	case dwarf.TagLexDwarfBlock, dwarf.TagSubprogram:
		// Recurse into blocks and functions, if the respective block contains
		// pc (or if we don't care about visibility).
		if (flags&VariablesOnlyVisible == 0) || root.ContainsPC(pc) {
			for _, child := range root.Children {
				v = variablesInternal(v, child, depth+1, pc, line, flags)
			}
		}
		return v
	default:
		// Variables are considered to be visible starting on the line after the
		// line they are declared on. Function arguments are an exception - the line
		// they are declared on does not matter; they are considered to be
		// visible throughout the function.
		declLine, varHasDeclLine := root.Val(dwarf.AttrDeclLine).(int64)
		checkDeclLine :=
			root.Tag != dwarf.TagFormalParameter && // var is not a function argument
				varHasDeclLine && // we know the DeclLine
				(flags&VariablesNoDeclLineCheck == 0) // we were not explicitly instructed to ignore DeclLine

		varVisible := !checkDeclLine || (line >= int(declLine)+1) // +1 because visibility starts on the line after DeclLine
		if varVisible {
			return append(v, Variable{root, depth})
		}
		return v
	}
}
