package main

var intc, intd int
func A(n int) {
	for i := 0; i < n; i++ {
		defer func() {
			intc+=10
			intd-=20
		}()
	}
	temp := intc
	intc = intd
	intd = temp
}

func main() {
	intc=0
	intd=0
	A(2)
}
