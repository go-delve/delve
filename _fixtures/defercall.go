package main

var n = 0

func sampleFunction() {
	n++
}

func callAndDeferReturn() {
	defer sampleFunction()
	sampleFunction()
	n++
}

func callAndPanic2() {
	defer sampleFunction()
	sampleFunction()
	panic("panicking")
}

func callAndPanic() {
	defer recover()
	callAndPanic2()
}

func main() {
	callAndDeferReturn()
	callAndPanic()
}
