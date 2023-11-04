package main

import (
	"fmt"
)

type Thing struct {
	str string
}

func (d *Thing) Test() bool {
	return d != nil
}

func callit(f func()) {
	f()
}

func main() {
	cases := []struct {
		name  string
		thing Thing
	}{
		{
			name:  "Success",
			thing: Thing{str: "hello"},
		},
	}

	for _, c := range cases {
		callit(func() {
			fmt.Println("hello", c.thing.Test())
		})
	}
}
