package main

// #cgo CFLAGS: -g -Wall -O0
/*
void sigsegv(int x) {
	int *p = NULL;
	*p = x;
}
void testfn(int x) {
	sigsegv(x);
}
*/
import "C"

func main() {
	C.testfn(C.int(10))
}
