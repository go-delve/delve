package main

/*
#include <stdio.h>
char* foo(void) { return "hello, world!"; }
*/
import "C"

import "fmt"
import "runtime"

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	fmt.Println(C.GoString(C.foo()))
}
