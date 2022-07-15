package main

/*
int a(int v) {
	return 0xff + v;
}
*/
import "C"
import "fmt"

func main() {
	fmt.Println("aaa")
	print(C.a(11))
}
