package main

import "fmt"

func main() {
	for {
		for i := 0; i < 5; i++ {
			if i == 0 {
				fmt.Println("it is zero!")
			} else if i == 1 {
				fmt.Println("it is one")
			} else {
				fmt.Println("wat")
			}
			switch i {
			case 3:
				fmt.Println("three")
			case 4:
				fmt.Println("four")
			}
		}
		fmt.Println("done")
	}
	{
		fmt.Println("useless line")
	}
	fmt.Println("end")
}

func noop() {
	var (
		i = 1
		j = 2
	)

	if j == 3 {
		fmt.Println(i)
	}

	fmt.Println(j)
}

func looptest() {
	for {
		fmt.Println("wat")
		if false {
			fmt.Println("uh, wat")
			break
		}
	}
	fmt.Println("dun")
}

func endlesslooptest() {
	for {
		fmt.Println("foo")
		fmt.Println("foo")
	}
}

func decltest() {
	var foo = "bar"
	var baz = 9
	fmt.Println(foo, baz)
}
