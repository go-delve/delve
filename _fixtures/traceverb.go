package main

// Simple types for testing trace verbosity
type Point struct {
	X int
}

type Rectangle struct {
	Color string
}

type Address struct {
	Street string
	City   string
}

type Person struct {
	Name    string
	Address *Address
}

// Test function with primitives - used for verbosity level 1
func testPrimitives(i int, f float64, s string, b bool) int {
	return i * 2
}

// Test function with struct - used for verbosity levels 2 and 3
func testStruct(p Point, r Rectangle) Point {
	return Point{X: p.X + 10}
}

// Test function with nested struct - used for verbosity level 4
func testNested(addr Address, person Person) string {
	return person.Name
}

func main() {
	// Test 1: Primitives
	testPrimitives(42, 3.14159, "Hello World", true)

	// Test 2: Struct
	p := Point{X: 10}
	r := Rectangle{Color: "red"}
	testStruct(p, r)

	// Test 3: Nested struct
	addr := Address{
		Street: "123 Main St",
		City:   "Springfield",
	}
	person := Person{
		Name:    "Alice",
		Address: &addr,
	}
	testNested(addr, person)
}
