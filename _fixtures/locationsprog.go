package main

import (
	"fmt"
	"io/ioutil"
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
	fmt.Printf("%s %s\n", a.String(), b.String())
	a.SomeFunction()
	anotherFunction()
	ioutil.ReadFile("nonexistent.file.txt")
}
