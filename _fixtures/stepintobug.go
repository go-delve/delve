package main

type T struct {
}

func main() {
	t := T{}
	f1 := t.m1
	println(f1(1)) //break
}

func (t T) m1(x int) int {
	return x + 1
}
