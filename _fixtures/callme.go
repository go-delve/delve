package main

import "fmt"

func callme(i int) {
	fmt.Println("got:", i)
}

const nBytes = 10
var zeroarr [nBytes]byte

func callme2() {
	for i := 0; i < nBytes; i++ {
		zeroarr[i] = '0'
	}
}

func callme3() {
	callme2()
}

func main() {
	for i := 0; i < 5; i++ {
		callme(i)
	}
	callme3()
}
