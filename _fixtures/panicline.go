package main

import "os"

func main() {
	fi, _ := os.Lstat("/this/path/does/not/exist")
	fi.Size()
}
