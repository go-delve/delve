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
	http.HandleFunc("/nobp", func(w http.ResponseWriter, req *http.Request) {
		msg := "hello, world!"
		header := w.Header().Get("Content-Type")
		w.Write([]byte(msg + header))
	})
	err := http.ListenAndServe(":9191", nil)
	if err != nil {
		panic(err)
	}
}
