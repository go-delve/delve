#include <stdio.h>

#include "_cgo_export.h"

#ifdef __amd64__
#define BREAKPOINT asm("int3;")
#elif __i386__
#define BREAKPOINT asm("int3;")
#elif __PPC64__
#define BREAKPOINT asm("tw 31,0,0;")
#elif __aarch64__
#ifdef WIN32
#define BREAKPOINT asm("brk 0xF000;")
#else
#define BREAKPOINT asm("brk 0;")
#endif
#elif __riscv
#define BREAKPOINT asm("ebreak;")
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
