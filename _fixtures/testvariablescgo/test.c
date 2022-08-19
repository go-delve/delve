#include <stdlib.h>
#include <string.h>
#include <stdio.h>

#ifdef __amd64__
#define BREAKPOINT asm("int3;")
#elif __i386__
#define BREAKPOINT asm("int3;")
#elif __aarch64__
#ifdef WIN32
#define BREAKPOINT asm("brk 0xF000;")
#else
#define BREAKPOINT asm("brk 0;")
#endif
#endif

#define N 100

struct align_check {
	int a;
	char b;
};

void testfn(void) {
	const char *s0 = "a string";
	const char *longstring = "averylongstring0123456789a0123456789b0123456789c0123456789d0123456789e0123456789f0123456789g0123456789h0123456789";
	int *v = malloc(sizeof(int) * N);
	struct align_check *v_align_check = malloc(sizeof(struct align_check) * N);

	for (int i = 0; i < N; i++) {
		v[i] = i;
		v_align_check[i].a = i;
		v_align_check[i].b = i;
	}

	char *s = malloc(strlen(s0) + 1);
	strcpy(s, s0);

	BREAKPOINT;
	
	printf("%s %s %p %p\n", s, longstring, v, v_align_check);
}
