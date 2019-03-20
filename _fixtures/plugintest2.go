package main

import (
	"fmt"
	"github.com/go-delve/delve/_fixtures/internal/pluginsupport"
	"os"
	"plugin"
)

type asomething struct {
	n int
}

func (a *asomething) Callback(n int) int {
	return a.n + n
}

func (a *asomething) String() string {
	return "success"
}

var ExeGlobal = &asomething{2}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	plug1, err := plugin.Open(os.Args[1])
	must(err)
	plug2, err := plugin.Open(os.Args[2])
	must(err)
	fn1iface, err := plug1.Lookup("HelloFn")
	must(err)
	fn2iface, err := plug2.Lookup("TypesTest")
	must(err)
	fn1 := fn1iface.(func(int) string)
	fn2 := fn2iface.(func(pluginsupport.Something) pluginsupport.SomethingElse)
	a := fn1(3)
	b := fn2(&asomething{2})
	fmt.Println(a, b, ExeGlobal)
}
