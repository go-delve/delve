package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		runtime.Breakpoint()
		msg := "hello, world!"
		header := w.Header().Get("Content-Type")
		w.Write([]byte(msg + header))
	})
	http.HandleFunc("/nobp", func(w http.ResponseWriter, req *http.Request) {
		msg := "hello, world!"
		header := w.Header().Get("Content-Type")
		w.Write([]byte(msg + header))
	})
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	fmt.Printf("LISTENING:%d\n", port)
	
	// Also write port to a file for tests that can't capture stdout
	// Include PID in filename to avoid conflicts when tests run in parallel
	portFile := fmt.Sprintf("/tmp/testnextnethttp_port_%d", os.Getpid())
	os.WriteFile(portFile, []byte(fmt.Sprintf("%d", port)), 0644)
	
	// Clean up port file when program exits
	defer os.Remove(portFile)
	
	err = http.Serve(listener, nil)
	if err != nil {
		panic(err)
	}
}
