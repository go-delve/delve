package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	buf, _ := io.ReadAll(os.Stdin)
	fmt.Fprintf(os.Stdout, "%s %v\n", buf, time.Now())
}
