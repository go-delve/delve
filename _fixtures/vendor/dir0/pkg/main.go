package pkg

type SomeType struct {
	X float64
}

func (s *SomeType) AMethod(x int) int {
	return x + 3
}

func (s *SomeType) AnotherMethod(x int) int {
	return x + 4
}
