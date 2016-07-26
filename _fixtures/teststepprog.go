package main

var n = 0

func CallFn2() {
	n++
}

func CallFn(fn func()) {
	fn()
}

func CallEface(eface interface{}) {
	if eface != nil {
		n++
	}
}

func main() {
	CallFn(CallFn2)
	CallEface(n)
}
