package gdbserial_test

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/go-delve/delve/pkg/proc/gdbserial"
	"io"
	"os"
	"testing"
)

const binary = "debugserver"
const cltPath = "/Library/Developer/CommandLineTools/Library/PrivateFrameworks/LLDB.framework/Versions/A/Resources/" + binary
const xcodePath = "/Applications/Xcode.app/Contents/SharedFrameworks/LLDB.framework/Versions/A/Resources/"

func TestIssue986_binaryFoundInSystemPath(t *testing.T) {
	cwd, _ := os.Getwd()
	var expected = cwd + "/bin/debugserver"
	var originalPath = os.Getenv("PATH")

	os.MkdirAll("bin", os.ModePerm)
	inPath, _ := os.Create(expected)
	inPath.Chmod(0777)
	inPath.Close()
	os.Setenv("PATH", cwd+"/bin:"+originalPath)
	var actual = gdbserial.GetDebugServerAbsolutePath()
	if actual != expected {
		t.Errorf("Expected %v, but got %v", expected, actual)
	}

	os.Setenv("PATH", originalPath)
	os.RemoveAll("./bin")
}

func TestIssue986_binaryFoundInXcodeBundle(t *testing.T) {
	var hash string
	var expected = xcodePath + binary

	if _, err := os.Stat(expected); err != nil {
		os.MkdirAll(xcodePath, os.ModePerm)
		d, _ := os.Create(expected)
		d.Chmod(0777)
		d.Close()

		h := sha256.New()
		s, _ := os.Open(expected)
		io.Copy(h, s)
		s.Close()

		src := h.Sum(nil)
		hash = hex.EncodeToString(src)
	}

	var actual = gdbserial.GetDebugServerAbsolutePath()
	if actual != expected {
		t.Errorf("Expected %v as the path, but got %v", expected, actual)
	}

	if s, err := os.Open(expected); err == nil {
		h := sha256.New()
		io.Copy(h, s)
		s.Close()

		src := h.Sum(nil)
		var f = hex.EncodeToString(src)
		if f == hash {
			os.RemoveAll(xcodePath)
		}
	}
}

func TestIssue986_binaryFoundInCltBundle(t *testing.T) {
	var actual = gdbserial.GetDebugServerAbsolutePath()
	if actual != cltPath {
		t.Errorf("Expected %v as the path, but got %v", cltPath, actual)
	}
}
