package main

import "fmt"

type FooBar struct {
	Baz int
	Bur string
}

func main() {
	var (
		bar = "foo"
		foo = 6
		sl  = []int{1, 2, 3, 4, 5}
		arr = [1]int{1}
		baz = &FooBar{Baz: 5, Bur: "strum"}
	)

	fmt.Println(bar, foo, arr, sl, baz)
}
