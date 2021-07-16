package main

import "fmt"

func issue2593(z interface{}) error {

	switch t := z.(type) {
	case string:
		if err := issue2593_call(); err != nil {
			// Will go here.
			return err
		}
	default:
		fmt.Print(t)
	}

	// Then here
	return nil
}

func issue2593_call() error {
	return nil
}

func main() {
	err := issue2593("test")
	// Will print <nil>
	fmt.Print(err)
}
