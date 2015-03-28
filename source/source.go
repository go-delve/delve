package source

import (
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
			n, err := s.FirstNodeAt(fname, 1)
			if err != nil {
				return nil, err
			}
			ast.Inspect(n, func(n ast.Node) bool {
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
			parents     []*ast.BlockStmt
			parentLines []int
			parentLine  int
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
			if stmt, ok := n.(*ast.ForStmt); ok {
				parents = append(parents, stmt.Body)
				pos := s.fileset.Position(stmt.Pos())
				parentLine = pos.Line
				parentLines = append(parentLines, pos.Line)
			}
			pos := s.fileset.Position(n.Pos())
			if line < pos.Line {
				if _, ok := n.(*ast.BlockStmt); ok {
					return true
				}
				for {
					if 0 < len(parents) {
						parent := parents[len(parents)-1]
						endLine := s.fileset.Position(parent.Rbrace).Line
						if endLine < line {
							if len(parents) == 1 {
								parents = []*ast.BlockStmt{}
								parentLines = []int{}
								parentLine = 0
							} else {
								parents = parents[0 : len(parents)-1]
								parentLines = parentLines[0:len(parents)]
								parent = parents[len(parents)-1]
								parentLine = s.fileset.Position(parent.Pos()).Line
							}
							continue
						}
						if parentLine != 0 {
							var endfound bool
							ast.Inspect(f, func(n ast.Node) bool {
								if n == nil || endfound {
									return false
								}
								if _, ok := n.(*ast.BlockStmt); ok {
									return true
								}
								pos := s.fileset.Position(n.Pos())
								if endLine < pos.Line {
									endLine = pos.Line
									endfound = true
									return false
								}
								return true
							})
							lines = append(lines, parentLine, endLine)
						}
					}
					break
				}
				if _, ok := n.(*ast.BranchStmt); !ok {
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
