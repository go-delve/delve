package main

import "fmt"

func testfn[T any](arg T) {
	fmt.Println(arg)
}

func main() {
	testfn[uint16](1)
	testfn[float64](2.1)
}
