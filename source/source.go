package source

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

type Searcher struct {
	fileset *token.FileSet
	visited map[string]*ast.File
}

func New() *Searcher {
	return &Searcher{fileset: token.NewFileSet(), visited: make(map[string]*ast.File)}
}

// Returns the first node at the given file:line.
func (s *Searcher) FirstNodeAt(fname string, line int) (ast.Node, error) {
	var node ast.Node
	f, err := s.parse(fname)
	if err != nil {
		return nil, err
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		position := s.fileset.Position(n.Pos())
		if position.Line == line {
			node = n
			return false
		}
		return true
	})
	if node == nil {
		return nil, fmt.Errorf("could not find node at %s:%d", fname, line)
	}
	return node, nil
}

type Done string

func (d Done) Error() string {
	return string(d)
}

// Returns all possible lines that could be executed after the given file:line,
// within the same source file.
func (s *Searcher) NextLines(fname string, line int) (lines []int, err error) {
	var found bool
	n, err := s.FirstNodeAt(fname, line)
	if err != nil {
		return nil, err
	}
	defer func() {
		if e := recover(); e != nil {
			e = e.(Done)
			nl := make([]int, 0, len(lines))
			fnd := make(map[int]bool)
			for _, l := range lines {
				if _, ok := fnd[l]; !ok {
					fnd[l] = true
					nl = append(nl, l)
				}
			}
			lines = nl
		}
	}()

	switch x := n.(type) {
	// Check if we are at an 'if' statement.
	//
	// If we are at an 'if' statement, employ the following algorithm:
	//    * Follow all 'else if' statements, appending their line number
	//    * Follow any 'else' statement if it exists, appending the line
	//      number of the statement following the 'else'.
	//    * If there is no 'else' statement, append line of first statement
	//      following the entire 'if' block.
	case *ast.IfStmt:
		var rbrace int
		p := x.Body.List[0].Pos()
		pos := s.fileset.Position(p)
		lines = append(lines, pos.Line)

		if x.Else == nil {
			// Grab first line after entire 'if' block
			rbrace = s.fileset.Position(x.Body.Rbrace).Line
			f, err := s.parse(fname)
			if err != nil {
				return nil, err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil {
					return true
				}
				pos := s.fileset.Position(n.Pos())
				if rbrace < pos.Line {
					lines = append(lines, pos.Line)
					panic(Done("done"))
				}
				return true
			})
		} else {
			// Follow any 'else' statements
			for {
				if stmt, ok := x.Else.(*ast.IfStmt); ok {
					pos := s.fileset.Position(stmt.Pos())
					lines = append(lines, pos.Line)
					x = stmt
					continue
				}
				pos := s.fileset.Position(x.Else.Pos())
				ast.Inspect(x, func(n ast.Node) bool {
					if found {
						panic(Done("done"))
					}
					if n == nil {
						return false
					}
					p := s.fileset.Position(n.Pos())
					if pos.Line < p.Line {
						lines = append(lines, p.Line)
						found = true
						return false
					}
					return true
				})
			}
		}

	// Follow case statements.
	//
	// Append line for first statement following each 'case' condition.
	case *ast.SwitchStmt:
		ast.Inspect(x, func(n ast.Node) bool {
			if stmt, ok := n.(*ast.SwitchStmt); ok {
				ast.Inspect(stmt, func(n ast.Node) bool {
					if stmt, ok := n.(*ast.CaseClause); ok {
						p := stmt.Body[0].Pos()
						pos := s.fileset.Position(p)
						lines = append(lines, pos.Line)
						return false
					}
					return true
				})
				panic(Done("done"))
			}
			return true
		})
	// Default case - find next source line.
	//
	// We are not at a branch, employ the following algorithm:
	//    * Traverse tree, storing any loop as a parent
	//    * Find next source line after the given line
	//    * Check and see if we've passed the scope of any parent we've
	//      stored. If so, pop them off the stack. The last parent that
	//      is left get's appending to our list of lines since we could
	//      end up at the top of the loop again.
	default:
		var (
			parents              []*ast.BlockStmt
			parentBlockBeginLine int
			deferEndLine         int
		)
		f, err := s.parse(fname)
		if err != nil {
			return nil, err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if found {
				panic(Done("done"))
			}
			if n == nil {
				return true
			}

			pos := s.fileset.Position(n.Pos())
			if line < pos.Line && deferEndLine != 0 {
				p := s.fileset.Position(n.Pos())
				if deferEndLine < p.Line {
					found = true
					lines = append(lines, p.Line)
					return false
				}
			}

			if stmt, ok := n.(*ast.ForStmt); ok {
				parents = append(parents, stmt.Body)
				pos := s.fileset.Position(stmt.Pos())
				parentBlockBeginLine = pos.Line
			}

			if _, ok := n.(*ast.GenDecl); ok {
				return true
			}

			if dn, ok := n.(*ast.DeferStmt); ok {
				endpos := s.fileset.Position(dn.End())
				deferEndLine = endpos.Line
				return false
			}

			if st, ok := n.(*ast.DeclStmt); ok {
				beginpos := s.fileset.Position(st.Pos())
				endpos := s.fileset.Position(st.End())
				if beginpos.Line < endpos.Line {
					return true
				}
			}

			// Check to see if we've found the "next" line.
			if line < pos.Line {
				if _, ok := n.(*ast.BlockStmt); ok {
					return true
				}
				var (
					parent        *ast.BlockStmt
					parentEndLine int
				)
				for len(parents) > 0 {
					parent = parents[len(parents)-1]

					// Grab the line number of the right brace of the parent block.
					parentEndLine = s.fileset.Position(parent.Rbrace).Line

					// Check to see if we're still within the parents block.
					// If we are, we're done and that is our parent.
					if parentEndLine > line {
						parentBlockBeginLine = s.fileset.Position(parent.Pos()).Line
						break
					}
					// If we weren't, and there is only 1 parent, we no longer have one.
					if len(parents) == 1 {
						parent = nil
						break
					}
					// Remove that parent from the stack.
					parents = parents[0 : len(parents)-1]
				}
				if parent != nil {
					var (
						endfound   bool
						beginFound bool
						beginLine  int
					)

					ast.Inspect(f, func(n ast.Node) bool {
						if n == nil || endfound {
							return false
						}
						if _, ok := n.(*ast.BlockStmt); ok {
							return true
						}
						pos := s.fileset.Position(n.Pos())
						if parentBlockBeginLine < pos.Line && !beginFound {
							beginFound = true
							beginLine = pos.Line
							return true
						}
						if parentEndLine < pos.Line {
							if _, ok := n.(*ast.FuncDecl); !ok {
								lines = append(lines, beginLine, pos.Line)
							}
							endfound = true
							return false
						}
						return true
					})
					lines = append(lines, parentBlockBeginLine)
				}
				switch n.(type) {
				case *ast.BranchStmt, *ast.FuncDecl:
				default:
					lines = append(lines, pos.Line)
				}
				found = true
				return false
			}
			return true
		})
		if len(lines) == 0 && 0 < len(parents) {
			parent := parents[len(parents)-1]
			lbrace := s.fileset.Position(parent.Lbrace).Line
			pos := s.fileset.Position(parent.List[0].Pos())
			lines = append(lines, lbrace, pos.Line)
		}
	}
	return lines, nil
}

// Parses file named by fname, caching files it has already parsed.
func (s *Searcher) parse(fname string) (*ast.File, error) {
	if f, ok := s.visited[fname]; ok {
		return f, nil
	}
	f, err := parser.ParseFile(s.fileset, fname, nil, 0)
	if err != nil {
		return nil, err
	}
	s.visited[fname] = f
	return f, nil
}
