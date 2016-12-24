package main

func PerfCheck(i, a, b, c int) {
	x := a - b - c
	_ = x
}

func main() {
	a := 1
	b := 1
	c := 1
	for i := 0; true; i++ {
		a = a * 2
		b = -b + i
		c = i * b
		PerfCheck(i, a, b, c)
	}
}
