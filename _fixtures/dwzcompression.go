package main

// #include <stdio.h>
// void fortytwo()
// {
//      fprintf(stdin, "42");
// }
import "C"
import "runtime"

func main() {
	C.fortytwo()
	runtime.Breakpoint()
}
