package main

import "fmt"

type Blah[T any] struct {
	x T
}

func (b *Blah[T]) F(y T) {
	b.x = y
}

type BlahInt interface {
	F(int)
}

func callf(b BlahInt) {
	b.F(2)
	fmt.Println(b)
}

func main() {
	b := &Blah[int]{10}
	callf(b)
}
