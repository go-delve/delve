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
		o := 0
		if root.Tag != dwarf.TagFormalParameter && (flags&VariablesTrustDeclLine != 0) {
			// visibility for variables starts the line after declaration line,
			// except for formal parameters, which are visible on the same line they
			// are defined.
			o = 1
		}
		if declLine, ok := root.Val(dwarf.AttrDeclLine).(int64); !ok || line >= int(declLine)+o {
			return append(v, Variable{root, depth})
		}
		return v
	}
}
