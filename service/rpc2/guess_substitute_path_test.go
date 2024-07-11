package rpc2

import (
	"runtime"
	"testing"
)

func TestMakeGuessSusbtitutePathIn(t *testing.T) {
	if runtime.GOARCH == "ppc64le" {
		t.Setenv("GOFLAGS", "-tags=exp.linuxppc64le")
	}
	gsp, err := MakeGuessSusbtitutePathIn()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%#v", gsp)
}
