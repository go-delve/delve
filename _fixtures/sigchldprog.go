package main

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
)

func main() {
	cmd := exec.Command("date")
	reader, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalln(err)
	}
	scanner := bufio.NewScanner(reader)
	go func() {
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
		reader.Close()
	}()
	cmd.Start()
	cmd.Wait()
}
