package main

import "fmt"

// Set the breakpoint at foo to catch it before it is initialized.
// The value will be invalid.
// (dlv) b testvariables3.go:18
// Breakpoint 1 set at 0x10c2651 for main.main() ./testvariables3.go:188
// (dlv) c
// > main.main() ./testvariables3.go:8 (hits goroutine(1):1 total:1) (PC: 0x10c2651)
//      17:	      func main() {
// =>   18:	           var foo interface{} = "foo" // Set breakpoint here
//      19:		   fmt.Println(foo)
// (dlv) locals
// foo = (unreadable invalid interface type: key not found)
//
func main() {
     var foo interface{} = "foo" // Set breakpoint here
     fmt.Println(foo)
}