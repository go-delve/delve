package main

import (
	"fmt"
	"runtime"
	"strings"
)

var (
	base = strings.Repeat("a", 1025)

	str         = base[:33]
	longStr     = base[:100]
	veryLongStr = base[:1025]

	bytes         = []byte(str)
	longBytes     = []byte(longStr)
	veryLongBytes = []byte(veryLongStr)
)

func f(argStr, argLongStr, argVeryLongStr string) {
	localStr := argStr
	localLongStr := argLongStr
	localVeryLongStr := argVeryLongStr

	runtime.Breakpoint()
	fmt.Println(str, longStr, veryLongStr, bytes, longBytes, veryLongBytes)
	fmt.Println(argStr, argLongStr, argVeryLongStr)
	fmt.Println(localStr, localLongStr, localVeryLongStr)
}

func main() {
	f(str, longStr, veryLongStr)
}
