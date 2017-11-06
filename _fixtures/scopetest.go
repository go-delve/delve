package main

func f1(x int) {}
func f2(x int) {}
func f3(x int) {}
func f4(x int) {}
func f5(x int) {}
func f6(x int) {}
func gret1(x int) int {
	return x - 1
}

var boolvar = true

func gretbool() bool {
	x := boolvar
	boolvar = !boolvar
	return x
}
func gret3() (int, int, int) { return 0, 1, 2 }

var v = []int{0, 1, 2}
var ch = make(chan int, 1)
var floatch = make(chan float64, 1)
var iface interface{} = 13

func TestNestedFor() {
	a := 0
	f1(a) // a int = 0
	for i := 0; i < 5; i++ {
		f2(i) // a int = 0, i int
		for i := 1; i < 5; i++ {
			f3(i) // a int = 0, i int = 0, i int = 1
			i++
			f3(i)
		}
		f4(i) // a int = 0, i int = 0
	}
	f5(a)
}
func TestOas2() {
	if a, b, c := gret3(); a != 1 {
		f1(a) // a int = 0, b int = 1, c int = 2
		f1(b) // a int = 0, b int = 1, c int = 2
		f1(c) // a int = 0, b int = 1, c int = 2
	}
	for i, x := range v {
		f1(i) // i int = 0, x int = 0
		f1(x) // i int = 0, x int = 0
	}
	if a, ok := <-ch; ok {
		f1(a) // a int = 12, ok bool = true
	}
	if a, ok := iface.(int); ok {
		f1(a) // a int = 13, ok bool = true
	}
}
func TestIfElse(x int) {
	if x := gret1(x); x != 0 {
		a := 0
		f1(a) // x int = 2, x int = 1, a int = 0
		f1(x)
	} else {
		b := 1
		f1(b) // x int = 1, x int = 0, b int = 1
		f1(x + 1)
	}
}
func TestSwitch(in int) {
	switch x := gret1(in); x {
	case 0:
		i := x + 5
		f1(x) // in int = 1, x int = 0, i int  = 5
		f1(i)
	case 1:
		j := x + 10
		f1(x)
		f1(j) // in int = 2, x int = 1, j int = 11
	case 2:
		k := x + 2
		f1(x)
		f1(k) // in int = 3, x int = 2, k int = 4
	}
}
func TestTypeSwitch(iface interface{}) {
	switch x := iface.(type) {
	case int:
		f1(x) // iface interface{}, x int = 1
	case uint8:
		f1(int(x)) // iface interface{}, x uint8 = 2
	case float64:
		f1(int(x) + 1) // iface interface{}, x float64 = 3.0
	}
}
func TestSelectScope() {
	select {
	case i := <-ch:
		f1(i) // i int = 13
	case f := <-floatch:
		f1(int(f)) // f float64 = 14.0
	}
}
func TestBlock() {
	a := 1
	f1(a) // a int = 1
	{
		b := 2
		a := 3
		f1(b) // a int = 1, a int = 3, b int = 2
		f1(a) // a int = 1, a int = 3, b int = 2
	}
}
func TestDiscontiguousRanges() {
	a := 0
	f1(a) // a int = 0
	{
		b := 0
		f2(b) // a int = 0, b int = 0
		if gretbool() {
			c := 0
			f3(c) // a int = 0, b int = 0, c int = 0
		} else {
			c := 1.1
			f4(int(c)) // a int = 0, b int = 0, c float64 = 1.1
		}
		f5(b) // a int = 0, b int  = 0
	}
	f6(a) // a int = 0
}
func TestClosureScope() {
	a := 1
	b := 1
	f := func(c int) {
		d := 3
		f1(a) // a int = 1, c int = 3, d int = 3
		f1(c)
		f1(d)
		if e := 3; e != 0 {
			f1(e) // a int = 1, c int = 3, d int = 3, e int = 3
		}
	}
	f(3)
	f1(b)
}
func main() {
	ch <- 12
	TestNestedFor()
	TestOas2()
	TestIfElse(2)
	TestIfElse(1)
	TestSwitch(3)
	TestSwitch(2)
	TestSwitch(1)
	TestTypeSwitch(1)
	TestTypeSwitch(uint8(2))
	TestTypeSwitch(float64(3.0))
	ch <- 13
	TestSelectScope()
	floatch <- 14.0
	TestSelectScope()
	TestBlock()
	TestDiscontiguousRanges()
	TestDiscontiguousRanges()
	TestClosureScope()
}
