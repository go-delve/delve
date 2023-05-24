package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func traceme1() {
	fmt.Printf("parent starting\n")
}

func traceme2(n string) {
	fmt.Printf("hello from %s\n", n)
}

func traceme3() {
	fmt.Printf("done\n")
}

func main() {
	exe, _ := os.Executable()
	switch os.Args[1] {
	case "spawn":
		traceme1()
		n, _ := strconv.Atoi(os.Args[2])
		cmds := []*exec.Cmd{}
		for i := 0; i < n; i++ {
			cmd := exec.Command(exe, "child", fmt.Sprintf("C%d", i))
			cmd.Stdout = os.Stdout
			cmd.Start()
			cmds = append(cmds, cmd)
		}

		for _, cmd := range cmds {
			cmd.Wait()
		}

		time.Sleep(1 * time.Second)
		traceme3()

	case "child":
		traceme2(os.Args[2])

	}
}
