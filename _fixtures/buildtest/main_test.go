package main

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestCurrentDirectory(t *testing.T) {
	wd, _ := os.Getwd()
	t.Logf("current directory: %s", wd)
}
