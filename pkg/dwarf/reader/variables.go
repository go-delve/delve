package reader

import (
	"debug/dwarf"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
)

type Variable struct {
	*godwarf.Tree
	Depth int
}

// Variables returns a list of variables contained inside 'root'.
// If onlyVisible is true only variables visible at pc will be returned.
// If skipInlinedSubroutines is true inlined subroutines will be skipped
func Variables(root *godwarf.Tree, pc uint64, line int, onlyVisible, skipInlinedSubroutines bool) []Variable {
	return variablesInternal(nil, root, 0, pc, line, onlyVisible, skipInlinedSubroutines)
}

func variablesInternal(v []Variable, root *godwarf.Tree, depth int, pc uint64, line int, onlyVisible, skipInlinedSubroutines bool) []Variable {
	switch root.Tag {
	case dwarf.TagInlinedSubroutine:
		if skipInlinedSubroutines {
			return v
		}
		fallthrough
	case dwarf.TagLexDwarfBlock, dwarf.TagSubprogram:
		if !onlyVisible || root.ContainsPC(pc) {
			for _, child := range root.Children {
				v = variablesInternal(v, child, depth+1, pc, line, onlyVisible, skipInlinedSubroutines)
			}
		}
		return v
	default:
		if declLine, ok := root.Val(dwarf.AttrDeclLine).(int64); !ok || line >= int(declLine) {
			return append(v, Variable{root, depth})
		}
		return v
	}
}
