package main

import (
	"fmt"
	"reflect"
)

var i = 2
var val = reflect.ValueOf(i)

func reflectFunc(value reflect.Value) {
	fmt.Printf("%s\n", value.Type().Name())
}

func main() {
	reflectFunc(val)
	fmt.Println(&i)
}
