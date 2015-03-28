package source

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"testing"
)

func TestTokenAtLine(t *testing.T) {
	var (
		tf, _ = filepath.Abs("../_fixtures/testvisitorprog.go")
		v     = New()
	)
	n, err := v.FirstNodeAt(tf, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := n.(*ast.IfStmt); !ok {
		t.Fatal("Did not get correct node")
	}
}

func TestNextLines(t *testing.T) {
	var (
		tf, _ = filepath.Abs("../_fixtures/testvisitorprog.go")
		v     = New()
	)
	cases := []struct {
		line      int
		nextlines []int
	}{
		{8, []int{9, 10, 13}},
		{15, []int{17, 19}},
		{25, []int{27}},
		{22, []int{6, 25}},
		{33, []int{36}},
		{36, []int{37, 40}},
		{47, []int{44, 51}},
		{57, []int{55, 56}},
	}
	for i, c := range cases {
		lines, err := v.NextLines(tf, c.line)
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != len(c.nextlines) {
			fmt.Println(lines)
			t.Fatalf("did not get correct number of lines back expected %d got %d for test case %d", len(c.nextlines), len(lines), i+1)
		}
		for i, l := range lines {
			if l != c.nextlines[i] {
				t.Fatalf("expected index %d to be %d got %d", i, c.nextlines[i], l)
			}
		}
	}
}
