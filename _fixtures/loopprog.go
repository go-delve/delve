package main

import "fmt"

func loop() {
	i := 0
	for {
		i++
		if (i % 100000) == 0 {
			fmt.Println(i)
		}
	}
	fmt.Println(i)
}

func main() {
	fmt.Println("past main")
	loop()
}
