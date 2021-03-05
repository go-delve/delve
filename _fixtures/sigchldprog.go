package main

import (
	"bufio"
	"fmt"
	"log"

	exec "golang.org/x/sys/execabs"
)

func main() {
	cmd := exec.Command("date")
	reader, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalln(err)
	}
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	go func() {
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
	}()
	cmd.Start()
	cmd.Wait()
}
