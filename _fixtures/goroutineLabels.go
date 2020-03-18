package main

import (
	"context"
	"runtime"
	"runtime/pprof"
)

func main() {
	ctx := context.Background()
	labels := pprof.Labels("k1", "v1", "k2", "v2")
	runtime.Breakpoint()
	pprof.Do(ctx, labels, f)
}

var dummy int

func f(ctx context.Context) {
	a := dummy
	runtime.Breakpoint()
	dummy++
	dummy = a
}
