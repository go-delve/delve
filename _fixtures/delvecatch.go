package main

import "fmt"

func main() {
	for i, iface := range []any{12, "test", nil, 2.2, "hello", 7} {
		fmt.Println(i, iface)
	}
}
