package main

/*
char* foo(void) { return "hello, world!"; }
*/
import "C"

import "fmt"

func main() {
	fmt.Println(C.GoString(C.foo()))
}
