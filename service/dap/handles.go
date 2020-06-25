package dap

// Based on
// https://github.com/microsoft/vscode-debugadapter-node/blob/master/adapter/src/handles.ts

const startHandle = 1000

type handles struct {
	nextHandle int
	handleMap  map[int]interface{}
}

func newHandles() *handles {
	hs := handles{startHandle, make(map[int]interface{})}
	return &hs
}

func (hs *handles) reset() {
	hs.nextHandle = startHandle
	hs.handleMap = make(map[int]interface{})
}

func (hs *handles) create(value interface{}) int {
	next := hs.nextHandle
	hs.nextHandle++
	hs.handleMap[next] = value
	return next
}

func (hs *handles) get(handle int) (interface{}, bool) {
	v, ok := hs.handleMap[handle]
	return v, ok
}
