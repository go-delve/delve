package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
	"strings"
)

// Long json for MemoryReference
func main() {
	payload := strings.Repeat("AB", 2500)

	b, _ := json.Marshal(struct {
		Data string `json:"data"`
	}{Data: payload})

	jsonString := string(b)

	hashed := sha256.Sum256(b)

	jsonHash := hex.EncodeToString(hashed[:])

	runtime.Breakpoint()
	_ = jsonString
	_ = jsonHash
}
