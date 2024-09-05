package main

import (
	"fmt"
	"iter"
	"strconv"
	"time"
)

func main() {
	set := New[string]()
	for i := 10; i < 100; i++ {
		set.Add(strconv.Itoa(i))
	}
	PrintAllElements[string](set)
}

// Set holds a set of elements.
type Set[E comparable] struct {
	m map[E]struct{}
}

// New returns a new [Set].
func New[E comparable]() *Set[E] {
	return &Set[E]{m: make(map[E]struct{})}
}

// All is an iterator over the elements of s.
func (s *Set[E]) All() iter.Seq[E] {
	return func(yield func(E) bool) {
		for v := range s.m {
			tmp := make([]byte, 1024)
			str := string(tmp)
			if !yield(v) {
				return
			}
			go func() { println(str) }()
		}
	}
}

func (s *Set[E]) Add(v E) {
	s.m[v] = struct{}{}
}

func PrintAllElements[E comparable](s *Set[E]) {
	for v := range s.All() {
		time.Sleep(100 * time.Second)
		fmt.Println(v)
	}
}
