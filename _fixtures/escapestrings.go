package main

import "runtime"

var (
	multiline    = "hello\nworld"
	withTabs     = "col1\tcol2\tcol3"
	mixed        = "line1\nline2\tindented\nline3"
	backslash    = "C:\\Users\\test"
	carriageRet  = "line1\r\nline2"
	withQuote    = "he said \"hello\""
	withNullByte = "before\x00after"
)

func getMultiline() string {
	return "func\nline1\nline2"
}

func getWithTabs() string {
	return "func\tcol1\tcol2"
}

func main() {
	// Use functions so they are available for `call` evaluation
	gm := getMultiline()
	gwt := getWithTabs()
	runtime.Breakpoint()
	println(multiline, withTabs, mixed, backslash, carriageRet, withQuote, withNullByte, gm, gwt)
}
