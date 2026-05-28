package main

// #include <stdio.h>
// int globalvar = 10;
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
