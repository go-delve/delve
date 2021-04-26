package dap

import "github.com/go-delve/delve/pkg/proc"

const startHandle = 1000

// handlesMap maps arbitrary values to unique sequential ids.
// This provides convenient abstraction of references, offering
// opacity and allowing simplification of complex identifiers.
// Based on
// https://github.com/microsoft/vscode-debugadapter-node/blob/master/adapter/src/handles.ts
type handlesMap struct {
	nextHandle  int
	handleToVal map[int]interface{}
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
	// Goroutine
	goid int
	// Frame
	frame int
	// True if the current variable is partial.
	isPartial bool
}

func newHandlesMap() *handlesMap {
	return &handlesMap{startHandle, make(map[int]interface{})}
}

func (hs *handlesMap) reset() {
	hs.nextHandle = startHandle
	hs.handleToVal = make(map[int]interface{})
}

func (hs *handlesMap) create(value interface{}) int {
	next := hs.nextHandle
	hs.nextHandle++
	hs.handleToVal[next] = value
	return next
}

func (hs *handlesMap) get(handle int) (interface{}, bool) {
	v, ok := hs.handleToVal[handle]
	return v, ok
}

func (hs *handlesMap) replace(oldHandle, newHandle int) bool {
	if _, ok := hs.get(oldHandle); !ok {
		return false
	}
	newValue, ok := hs.get(newHandle)
	if !ok {
		return false
	}
	hs.handleToVal[oldHandle] = newValue
	return true
}

type variablesHandlesMap struct {
	m *handlesMap
}

func newVariablesHandlesMap() *variablesHandlesMap {
	return &variablesHandlesMap{newHandlesMap()}
}

func (hs *variablesHandlesMap) create(value *fullyQualifiedVariable) int {
	return hs.m.create(value)
}

func (hs *variablesHandlesMap) get(handle int) (*fullyQualifiedVariable, bool) {
	v, ok := hs.m.get(handle)
	if !ok {
		return nil, false
	}
	return v.(*fullyQualifiedVariable), true
}

func (hs *variablesHandlesMap) reset() {
	hs.m.reset()
}
