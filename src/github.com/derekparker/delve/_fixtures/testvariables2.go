package main

import "runtime"

type a struct {
	aas []a
}

func main() {
	var aas = []a{{nil}}
	aas[0].aas = aas
	runtime.Breakpoint()
}
