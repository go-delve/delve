package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"unsafe"
)

// Long json for MemoryReference
func main() {
	runtime.Breakpoint()

	payload := strings.Repeat("AB", 2500)

	b, _ := json.Marshal(struct {
		Data string `json:"data"`
	}{Data: payload})

	bytesString := []byte("this\nis\nit")
	nonprint := []byte{242, 243, 244, 245}
	maps := map[string]string{"some": "non"}

	jsonString := string(b)

	hashed := sha256.Sum256(b)
	jsonHash := hex.EncodeToString(hashed[:]) // used to validate fullness of a string

	ptr := unsafe.StringData(jsonString)
	jsonAddr := fmt.Sprintf("%p", ptr) // used to validate string address

	runtime.Breakpoint()

	fmt.Println(jsonString)
	fmt.Println(jsonHash)
	fmt.Println(jsonAddr)
	fmt.Println(bytesString)
	fmt.Println(nonprint)
	fmt.Println(maps)
}
