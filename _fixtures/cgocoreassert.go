package main

/*
#include <stdio.h>
#include <assert.h>

void test1(void) {
    assert(0);
}

void test2(void) {
    int val = 2;
    test1();
}

void test3(void) {
    int val = 3;
    test2();
}
*/
import "C"

func main() {
	C.test3()
}
