package main

/*
#cgo LDFLAGS: -framework CoreFoundation
#cgo LDFLAGS: -framework CFNetwork
#include <CFNetwork/CFProxySupport.h>
*/
import "C"
import "fmt"

func main() {
	f() // break here
}

func f() {
	fmt.Println("ok")
}
