package native_test

import (
	"testing"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/native"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestMain(m *testing.M) {
	protest.RunTestsWithFixtures(m)
}

func TestAttachLegacySignature(t *testing.T) {
	// Keep the original exported API assignable for downstream callers.
	var attach func(int, *proc.WaitFor, []string) (*proc.TargetGroup, error) = native.Attach
	if attach == nil {
		t.Fatal("Attach must be available")
	}
}
