package main

import (
	"fmt"
	"go/constant"
	"runtime"
	"unsafe"
)

type astruct struct {
	A int
	B int
}

type bstruct struct {
	a astruct
}

type cstruct struct {
	pb *bstruct
	sa []*astruct
}

func afunc(x int) int {
	return x + 2
}

type functype func(int) int

func (a *astruct) Error() string {
	return "not an error"
}

func (b *bstruct) Error() string {
	return "not an error"
}

func main() {
	i1 := 1
	i2 := 2
	f1 := 3.0
	i3 := 3
	p1 := &i1
	s1 := []string{"one", "two", "three", "four", "five"}
	s3 := make([]int, 0, 6)
	a1 := [5]string{"one", "two", "three", "four", "five"}
	c1 := cstruct{&bstruct{astruct{1, 2}}, []*astruct{&astruct{1, 2}, &astruct{2, 3}, &astruct{4, 5}}}
	s2 := []astruct{{1, 2}, {3, 4}, {5, 6}, {7, 8}, {9, 10}, {11, 12}, {13, 14}, {15, 16}}
	p2 := &(c1.sa[2].B)
	as1 := astruct{1, 1}
	var p3 *int
	str1 := "01234567890"
	var fn1 functype = afunc
	var fn2 functype = nil
	var nilslice []int = nil
	var nilptr *int = nil
	ch1 := make(chan int, 2)
	var chnil chan int = nil
	m1 := map[string]astruct{
		"Malone":          astruct{2, 3},
		"Adenauer":        astruct{},
		"squadrons":       astruct{},
		"quintuplets":     astruct{},
		"parasite":        astruct{},
		"wristwatches":    astruct{},
		"flashgun":        astruct{},
		"equivocally":     astruct{},
		"sweetbrier":      astruct{},
		"idealism":        astruct{},
		"tangos":          astruct{},
		"alterable":       astruct{},
		"quaffing":        astruct{},
		"arsenic":         astruct{},
		"coincidentally":  astruct{},
		"hindrances":      astruct{},
		"zoning":          astruct{},
		"egging":          astruct{},
		"inserts":         astruct{},
		"adaptive":        astruct{},
		"orientations":    astruct{},
		"periling":        astruct{},
		"lip":             astruct{},
		"chant":           astruct{},
		"availing":        astruct{},
		"fern":            astruct{},
		"flummoxes":       astruct{},
		"meanders":        astruct{},
		"ravenously":      astruct{},
		"reminisce":       astruct{},
		"snorkel":         astruct{},
		"gutters":         astruct{},
		"jibbed":          astruct{},
		"tiara":           astruct{},
		"takers":          astruct{},
		"animates":        astruct{},
		"Zubenelgenubi":   astruct{},
		"bantering":       astruct{},
		"tumblers":        astruct{},
		"horticulturists": astruct{},
		"thallium":        astruct{},
	}
	var mnil map[string]astruct = nil
	m2 := map[int]*astruct{1: &astruct{10, 11}}
	m3 := map[astruct]int{{1, 1}: 42, {2, 2}: 43}
	up1 := unsafe.Pointer(&i1)
	i4 := 800
	i5 := -3
	i6 := -500
	var err1 error = c1.sa[0]
	var err2 error = c1.pb
	var errnil error = nil
	var iface1 interface{} = c1.sa[0]
	var iface2 interface{} = "test"
	var iface3 interface{} = map[string]constant.Value{}
	var iface4 interface{} = []constant.Value{constant.MakeInt64(4)}
	var ifacenil interface{} = nil
	arr1 := [4]int{0, 1, 2, 3}
	parr := &arr1
	cpx1 := complex(1, 2)
	const1 := constant.MakeInt64(3)

	var amb1 = 1
	runtime.Breakpoint()
	for amb1 := 0; amb1 < 10; amb1++ {
		fmt.Println(amb1)
	}
	runtime.Breakpoint()
	fmt.Println(i1, i2, i3, p1, amb1, s1, s3, a1, p2, p3, s2, as1, str1, f1, fn1, fn2, nilslice, nilptr, ch1, chnil, m1, mnil, m2, m3, up1, i4, i5, i6, err1, err2, errnil, iface1, iface2, ifacenil, arr1, parr, cpx1, const1, iface3, iface4)
}
