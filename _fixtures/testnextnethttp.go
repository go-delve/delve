package main

import (
	"net/http"
	"runtime"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		runtime.Breakpoint()
		msg := "hello, world!"
		header := w.Header().Get("Content-Type")
		w.Write([]byte(msg + header))
	})
	http.ListenAndServe(":8080", nil)
}
