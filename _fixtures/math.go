package main

import "math"

var f = 1.5

func main() {
	floatvar1 := math.Floor(f)
	floatvar2 := float64(int(f))
	_ = floatvar1
	_ = floatvar2
}
