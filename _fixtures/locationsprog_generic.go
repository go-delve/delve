package main

import "fmt"

type ParamReceiver[T any] struct {
	field T
}

func (r *ParamReceiver[T]) Amethod() {
	fmt.Printf("%v\n", r.field)
}

func ParamFunc[T any](arg T) {
	fmt.Printf("%v\n", arg)
}

func main() {
	var x ParamReceiver[int]
	var y ParamReceiver[float64]
	x.Amethod()
	y.Amethod()
	ParamFunc[int](2)
	ParamFunc[float32](2)
}
