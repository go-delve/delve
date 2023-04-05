package main

import (
	"fmt"
	"reflect"
)

func reflectFunc(value reflect.Value) {
	fmt.Printf("%s\n", value.Type().Name())
}

func main() {
	i := 2
	val := reflect.ValueOf(i)
	reflectFunc(val)
}
