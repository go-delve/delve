package main

import (
	"fmt"
	"runtime"
)

type A struct {
}

func (a *A) Model() string {
	return "A"
}

type B struct {
	*A
	Model string
}

type A1 struct {
	X int
}

type B1 struct {
	*A1
}

func (*B1) X() {}

type A2 struct {
	X int
}

type B2 struct {
	*A2
}

func (*B2) X() {}

type C2 struct {
	*B2
}

type A3 struct {
	X int
}

type B3 struct {
	*A3
}

func (b *B3) X() {}

type C3 struct {
	*B3
}

type TestX interface {
	X()
}

type A4 struct {
}

func (a *A4) X() {}

type B4 struct {
	TestX
}

type XFunc = func() string

type A5 struct {
	X XFunc
}

type B5 struct {
	A5
}

type C5 struct {
	*B5
	X int
}

type Chan = chan struct{}

type A6 struct {
	Chan
}

type B7 struct {
}

func (b *B7) X() {}

type A7 struct {
	*B7
}

type TestX1 interface {
	X()
}

type B8 struct {
}

func (*B8) X() {}

type A8 struct {
	TestX
}

type B9 struct {
}

func (*B9) X() {}

type A9 struct {
	V TestX
}

type B10 struct {
	X int
}

type A10 struct {
	B10
}

func main() {
	v := &B{Model: "B", A: &A{}}
	fmt.Println(v.Model, v.A.Model)

	v1 := &B1{A1: &A1{}}
	fmt.Println(v1.X, v1.A1.X)

	v2 := &C2{B2: &B2{A2: &A2{}}}
	fmt.Println(v2.X, v2.B2.X, v2.B2.A2.X, v2.A2.X)

	v3 := &C3{B3: &B3{A3: &A3{}}}
	fmt.Println(v3.X, v3.A3.X, v3.B3.X, v3.B3.A3.X)

	v4 := &B4{TestX: &A4{}}
	fmt.Println(v4.X, v4.TestX.X)

	v5 := C5{B5: &B5{A5: A5{X: func() string { return "anonymous func" }}}}
	fmt.Println(v5.X, v5.B5.X, v5.B5.A5.X, v5.A5.X)

	ch := make(chan int)
	fmt.Println(ch)

	v6 := A6{Chan: make(chan struct{})}
	fmt.Println(v6.Chan)

	v7 := A7{}
	fmt.Print(v7.X, v7.B7.X)

	var x TestX
	x = &A7{}
	fmt.Println(x.X, x.(*A7).X, x.(*A7).B7.X)

	var x1 TestX

	fmt.Println(x1)

	var (
		x2 TestX
		x3 TestX1
	)

	x3 = &A7{}
	x2 = x3
	fmt.Println(x2.X, x3.X)

	v8 := A8{TestX: &B8{}}
	fmt.Println(v8)

	v9 := A9{V: &B9{}}
	fmt.Println(v9.V.X)

	a := struct {
		TestX
	}{
		TestX: TestX(v8),
	}

	fmt.Println(a.X)

	b := struct {
		TestX
	}{
		TestX: TestX(nil),
	}

	fmt.Println(b)

	c := struct {
		A10
		TestX
	}{
		A10:   A10{B10{X: 1}},
		TestX: TestX(&v8),
	}

	fmt.Println(c.X)

	d := struct {
		A10
		TestX
	}{
		A10:   A10{B10{X: 1}},
		TestX: TestX(nil),
	}

	fmt.Println(d)

	runtime.Breakpoint() // breakpoint here
}
