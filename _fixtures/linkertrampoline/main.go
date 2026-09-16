package main

import (
	"runtime"

	"github.com/go-delve/delve/_fixtures/linkertrampoline/callee"
)

func main() {
	runtime.Breakpoint()
	callee.Call()
}
