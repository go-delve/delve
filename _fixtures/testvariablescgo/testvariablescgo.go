package main

// #cgo CFLAGS: -g -Wall -O0 -std=gnu99
/*
extern void testfn(void);
*/
import "C"

func main() {
	C.testfn()
}
