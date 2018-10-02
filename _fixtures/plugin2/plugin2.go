package main

import (
	"fmt"
	"github.com/go-delve/delve/_fixtures/internal/pluginsupport"
)

func Fn2() string {
	return "world"
}

type asomethingelse struct {
	x, y float64
}

func (a *asomethingelse) Callback2(n, m int) float64 {
	r := a.x + 2*a.y
	r += float64(n) / float64(m)
	return r
}

func TypesTest(s pluginsupport.Something) pluginsupport.SomethingElse {
	if A != nil {
		aIsNotNil(fmt.Sprintf("%s", A))
	}
	return &asomethingelse{1.0, float64(s.Callback(2))}
}

var A interface{}

func aIsNotNil(str string) {
	// nothing here
}
