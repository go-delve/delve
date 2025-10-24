package main

import "fmt"

//go:noinline
func ManyArgsWithNamedReturns(a, b, c, d, e, f, g, h, i, j, k, l, m, n, o, p int) (sum int, product int) {
	sum = a + b + c + d + e + f + g + h
	product = 1

	temp1 := i * j
	temp2 := k * l
	temp3 := m * n
	temp4 := o * p

	sum += temp1 + temp2 + temp3 + temp4

	product = (a + 1) * (b + 1) * (c + 1) * (d + 1)
	product += (e + f) * (g + h) * (i + j) * (k + l)
	product += (m + n) * (o + p)

	sum = sum * 2
	product = product / 2

	return sum, product
}

func main() {
	sum, product := ManyArgsWithNamedReturns(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16)
	fmt.Println(sum, product)
}
