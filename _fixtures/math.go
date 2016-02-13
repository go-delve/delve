package main

import "math"

var f = 1.5

func main() {
	_ = math.Floor(f)
	_ = float64(int(f))
}
