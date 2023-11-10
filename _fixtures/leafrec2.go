package main
import "fmt"
func B(i int) int {
        return A(i,i)
}
func A(i int, n int) int {
	if(n==1) {
		return i
	} else {
		n--
		return (i*A(i-1,n))
	}
}
func main() {
	j := 0
	j += B(5)
	fmt.Println(j)
}
