package starbind

import (
	"go.starlark.net/starlark"
	"testing"
)

func TestConv(t *testing.T) {
	script := `
# A list global that we'll unmarshal into a slice.
x = [1,2]
`
	globals, err := starlark.ExecFile(&starlark.Thread{}, "test.star", script, nil)
	starlarkVal, ok := globals["x"]
	if !ok {
		t.Fatal("missing global 'x'")
	}
	if err != nil {
		t.Fatal(err)
	}
	var x []int
	err = unmarshalStarlarkValue(starlarkVal, &x, "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(x) != 2 || x[0] != 1 || x[1] != 2 {
		t.Fatalf("expected [1 2], got: %v", x)
	}
}
