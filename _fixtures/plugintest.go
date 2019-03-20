package main

import (
	"fmt"
	"os"
	"plugin"
	"runtime"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	plug1, err := plugin.Open(os.Args[1])
	must(err)

	runtime.Breakpoint()

	plug2, err := plugin.Open(os.Args[2])
	must(err)

	runtime.Breakpoint()

	fn1, err := plug1.Lookup("Fn1")
	must(err)
	fn2, err := plug2.Lookup("Fn2")
	must(err)

	a := fn1.(func() string)()
	b := fn2.(func() string)()

	fmt.Println(plug1, plug2, fn1, fn2, a, b)
}
