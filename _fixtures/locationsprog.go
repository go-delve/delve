package main

import (
	"fmt"
	"io/ioutil"
	"runtime"
	"unsafe"
)

type SomeType struct {
}

type OtherType struct {
}

func (a *SomeType) String() string {
	return "SomeTypeObject"
}

func (a *OtherType) String() string {
	return "OtherTypeObject"
}

func (a *SomeType) SomeFunction() {
	fmt.Printf("SomeFunction called\n")
}

func anotherFunction() {
	fmt.Printf("anotherFunction called\n")
}

func main() {
	var a SomeType
	var b OtherType
	i := 10
	fmt.Printf("%s %s %v\n", a.String(), b.String(), i)
	a.SomeFunction()
	anotherFunction()
	ioutil.ReadFile("nonexistent.file.txt")

	//Issue #1817
	bs := make([]byte, 100)
	p := uintptr(unsafe.Pointer(&bs))
	fmt.Println(p)
	runtime.KeepAlive(bs)
}

var amap map[string]func()

func init() {
	amap = map[string]func(){
		"k": func() {
			fmt.Printf("hello world")
		},
	}
}
