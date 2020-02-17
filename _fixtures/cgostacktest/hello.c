#include <stdio.h>

#include "_cgo_export.h"

#ifdef __amd64__
#define BREAKPOINT asm("int3;")
#elif __i386__
#define BREAKPOINT asm("int3;")
#elif __aarch64__
#define BREAKPOINT asm("brk 0;")
#endif

void helloworld_pt2(int x) {
	BREAKPOINT;
	helloWorld(x+1);
}

void helloworld(int x) {
	helloworld_pt2(x+1);
}

void helloworld_pt4(int x) {
	BREAKPOINT;
	helloWorld2(x+1);
}

void helloworld_pt3(int x) {
	helloworld_pt4(x+1);
}
