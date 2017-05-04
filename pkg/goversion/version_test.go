package goversion

import (
	"runtime"
	"testing"
)

func versionAfterOrEqual(t *testing.T, verStr string, ver GoVersion) {
	pver, ok := Parse(verStr)
	if !ok {
		t.Fatalf("Could not parse version string <%s>", verStr)
	}
	if !pver.AfterOrEqual(ver) {
		t.Fatalf("Version <%s> parsed as %v not after %v", verStr, pver, ver)
	}
	t.Logf("version string <%s> → %v", verStr, ver)
}

func TestParseVersionString(t *testing.T) {
	versionAfterOrEqual(t, "go1.4", GoVersion{1, 4, 0, 0, 0, ""})
	versionAfterOrEqual(t, "go1.5.0", GoVersion{1, 5, 0, 0, 0, ""})
	versionAfterOrEqual(t, "go1.4.2", GoVersion{1, 4, 2, 0, 0, ""})
	versionAfterOrEqual(t, "go1.5beta2", GoVersion{1, 5, -1, 2, 0, ""})
	versionAfterOrEqual(t, "go1.5rc2", GoVersion{1, 5, -1, 0, 2, ""})
	versionAfterOrEqual(t, "go1.6.1 (appengine-1.9.37)", GoVersion{1, 6, 1, 0, 0, ""})
	versionAfterOrEqual(t, "go1.8.1.typealias", GoVersion{1, 6, 1, 0, 0, ""})
	ver, ok := Parse("devel +17efbfc Tue Jul 28 17:39:19 2015 +0000 linux/amd64")
	if !ok {
		t.Fatalf("Could not parse devel version string")
	}
	if !ver.IsDevel() {
		t.Fatalf("Devel version string not correctly recognized")
	}
}

func TestInstalled(t *testing.T) {
	installedVersion, ok := Installed()
	if !ok {
		t.Fatalf("could not parse output of go version")
	}
	runtimeVersion, ok := Parse(runtime.Version())
	if !ok {
		t.Fatalf("could not parse output of runtime.Version() %q", runtime.Version())
	}

	t.Logf("installed: %v", installedVersion)
	t.Logf("runtime: %v", runtimeVersion)

	if installedVersion != runtimeVersion {
		t.Fatalf("version mismatch %#v %#v", installedVersion, runtimeVersion)
	}
}
