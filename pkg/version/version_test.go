package version

import "testing"

func TestBuildInfo(t *testing.T) {
	t.Error(BuildInfo())
}
