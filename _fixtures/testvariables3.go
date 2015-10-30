package main

import (
	"fmt"
	"runtime"
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

func main() {
	i1 := 1
	i2 := 2
	f1 := 3.0
	i3 := 3
	p1 := &i1
	s1 := []string{"one", "two", "three", "four", "five"}
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

	var amb1 = 1
	runtime.Breakpoint()
	for amb1 := 0; amb1 < 10; amb1++ {
		fmt.Println(amb1)
	}
	fmt.Println(i1, i2, i3, p1, amb1, s1, a1, p2, p3, s2, as1, str1, f1, fn1, fn2, nilslice, nilptr)
}
