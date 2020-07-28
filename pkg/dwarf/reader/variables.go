package reader

import (
	"debug/dwarf"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
)

type Variable struct {
	*godwarf.Tree
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
// If onlyVisible is true only variables visible at pc will be returned.
// If skipInlinedSubroutines is true inlined subroutines will be skipped
func Variables(root *godwarf.Tree, pc uint64, line int, flags VariablesFlags) []Variable {
	return variablesInternal(nil, root, 0, pc, line, flags)
}

func variablesInternal(v []Variable, root *godwarf.Tree, depth int, pc uint64, line int, flags VariablesFlags) []Variable {
	switch root.Tag {
	case dwarf.TagInlinedSubroutine:
		if flags&VariablesSkipInlinedSubroutines != 0 {
			return v
		}
		fallthrough
	case dwarf.TagLexDwarfBlock, dwarf.TagSubprogram:
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
