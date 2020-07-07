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

type variablesHandlesMap struct {
	m *handlesMap
}

func newVariablesHandlesMap() *variablesHandlesMap {
	return &variablesHandlesMap{newHandlesMap()}
}

func (hs *variablesHandlesMap) create(value *proc.Variable) int {
	return hs.m.create(value)
}

func (hs *variablesHandlesMap) get(handle int) (*proc.Variable, bool) {
	v, ok := hs.m.get(handle)
	if !ok {
		return nil, false
	}
	return v.(*proc.Variable), true
}

func (hs *variablesHandlesMap) reset() {
	hs.m.reset()
}
