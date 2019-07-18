package main

import (
	"runtime"
)

/*
typedef struct Qst Q1;
typedef const Q1 Q;
struct Qst {
	Q *q;
};

const Q1 globalq;
*/
import "C"

func main() {
	runtime.Breakpoint()
}
