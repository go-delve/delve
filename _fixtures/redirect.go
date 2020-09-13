package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"time"
)

func main() {
	buf, _ := ioutil.ReadAll(os.Stdin)
	fmt.Fprintf(os.Stdout, "%s %v\n", buf, time.Now())
}
