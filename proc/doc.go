// proc is a low-level package that provides methods to manipulate
// the process we are debugging, and methods to read and write from
// the virtual memory of the process.
//
// proc implements the core features of this debugger, including all
// process manipulation (step, next, continue, halt) as well as providing
// methods to evaluate variables and read them from the virtual memory of
// the process we are debugging.
//
package proc
