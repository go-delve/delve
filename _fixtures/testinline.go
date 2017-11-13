package main

import "fmt"

func inlineThis(a int) int {
	z := a * a
	return z + a/a
}

func initialize(a, b *int) {
	*a = 3
	*b = 4
}

func main() {
	var a, b int
	initialize(&a, &b)
	a = inlineThis(a)
	b = inlineThis(b)
	fmt.Printf("%d %d\n", a, b)
}
