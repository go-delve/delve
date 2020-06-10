package main

func main() {
	f(nil) //break
	println("ok")
}

func f(x *int) {
	println(*x)
}
