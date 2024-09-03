package dap

import "github.com/go-delve/delve/pkg/proc"

const startHandle = 1000

// handlesMap maps arbitrary values to unique sequential ids.
// This provides convenient abstraction of references, offering
// opacity and allowing simplification of complex identifiers.
// Based on
// https://github.com/microsoft/vscode-debugadapter-node/blob/master/adapter/src/handles.ts
type handlesMap[T any] struct {
	nextHandle  int
	handleToVal map[int]T
}

type fullyQualifiedVariable struct {
	*proc.Variable
	// A way to load this variable by either using all names in the hierarchic
	// sequence above this variable (most readable when referenced by the UI)
	// if available or a special expression based on:
	// https://github.com/go-delve/delve/blob/master/Documentation/api/ClientHowto.md#loading-more-of-a-variable
	// Empty if the variable cannot or should not be reloaded.
	fullyQualifiedNameOrExpr string
	// True if this represents variable scope
	isScope bool
	// startIndex is the index of the first child for an array or slice.
	// This variable represents a chunk of the array, slice or map.
	startIndex int
}

func newHandlesMap[T any]() *handlesMap[T] {
	return &handlesMap[T]{startHandle, make(map[int]T)}
}

func (hs *handlesMap[T]) reset() {
	hs.nextHandle = startHandle
	hs.handleToVal = make(map[int]T)
}

func (hs *handlesMap[T]) create(value T) int {
	next := hs.nextHandle
	hs.nextHandle++
	hs.handleToVal[next] = value
	return next
}

func (hs *handlesMap[T]) get(handle int) (T, bool) {
	v, ok := hs.handleToVal[handle]
	return v, ok
}
