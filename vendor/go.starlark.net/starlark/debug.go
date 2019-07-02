package starlark

// This file defines an experimental API for the debugging tools.
// Some of these declarations expose details of internal packages.
// (The debugger makes liberal use of exported fields of unexported types.)
// Breaking changes may occur without notice.

// Local returns the value of the i'th local variable.
// It may be nil if not yet assigned.
//
// Local may be called only for frames whose Callable is a *Function (a
// function defined by Starlark source code), and only while the frame
// is active; it will panic otherwise.
//
// This function is provided only for debugging tools.
//
// THIS API IS EXPERIMENTAL AND MAY CHANGE WITHOUT NOTICE.
func (fr *Frame) Local(i int) Value { return fr.locals[i] }
