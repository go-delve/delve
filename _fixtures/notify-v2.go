package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"
)

func main() {
	http.HandleFunc("/test", func(w http.ResponseWriter, req *http.Request) {
		go func() {
			// I know this is wrong, it is just to simulate a deadlocked goroutine
			fmt.Println("locking...")
			mtx := &sync.Mutex{}
			mtx.Lock()
			mtx.Lock()
			fmt.Println("will never print this")
		}()
	})

	log.Fatalln(http.ListenAndServe("127.0.0.1:8888", nil))
}
