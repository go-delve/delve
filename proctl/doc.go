// proctl is a low-level package that provides methods to manipulate
// the process we are debugging, and methods to read and write from
// the virtual memory of the process.
//
// proctl implements the core features of this debugger, including all
// process manipulation (step, next, continue, halt) as well as providing
// methods to evaluate variables and read them from the virtual memory of
// the process we are debugging.
//
// What follows is a breakdown of the division of responsibility by file:
//
// * proctl(_*).go/c - Data structures and methods for manipulating an entire process.
// * threads(_*).go/c - Data structures and methods for manipulating individual threads.
// * variablges.go - Data structures and methods for evaluation of variables.
// * breakpoints(_*).go - Data structures and methods for setting / clearing breakpoints.
// * registers_*.go - Data structures and methods for obtaining register information.
// * stack.go - Functions for unwinding the stack.
// * ptrace_*.go - Ptrace stubs for missing stdlib functionality.
//
package proctl
