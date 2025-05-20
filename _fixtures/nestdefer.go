package main

var varc int

func C() {
	defer D()
	varc += 10 * 10
}
func A() {
	defer C()
	varc = 40
}

func D() {
	B()
}

func B() {
	varc -= 20
}
func main() {
	A()
}
