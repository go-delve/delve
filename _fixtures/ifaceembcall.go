package main

import "fmt"

type A struct {
	a int
}

type B struct {
	*A
}

type Iface interface {
	PtrReceiver() string
	NonPtrReceiver() string
}

func (*A) PtrReceiver() string {
	return "blah"
}

func (A) NonPtrReceiver() string {
	return "blah"
}

func main() {
	var iface Iface = &B{&A{1}}
	s := iface.PtrReceiver()
	s = iface.NonPtrReceiver()
	fmt.Printf("%s\n", s)
}
