package main

var n = 0

func CallFn2(x int) {
	n++
}

func CallFn(x int, fn func(x int)) {
	fn(x + 1)
}

func CallEface(eface interface{}) {
	if eface != nil {
		n++
	}
}

func main() {
	CallFn(0, CallFn2)
	CallEface(n)
}
