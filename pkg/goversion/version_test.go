package goversion

import (
	"runtime"
	"testing"
)

func parseVer(t *testing.T, verStr string) GoVersion {
	pver, ok := Parse(verStr)
	if !ok {
		t.Fatalf("Could not parse version string <%s>", verStr)
	}
	return pver
}

func versionAfterOrEqual(t *testing.T, verStr string, ver GoVersion) {
	t.Helper()
	pver := parseVer(t, verStr)
	if !pver.AfterOrEqual(ver) {
		t.Fatalf("Version <%s> parsed as %v not after %v", verStr, pver, ver)
	}
	t.Logf("version string <%s> → %v", verStr, ver)
}

func versionAfterOrEqual2(t *testing.T, verStr1, verStr2 string) {
	t.Helper()
	pver1 := parseVer(t, verStr1)
	pver2 := parseVer(t, verStr2)
	if !pver1.AfterOrEqual(pver2) {
		t.Fatalf("Version <%s> %#v not after or equal to <%s> %#v", verStr1, pver1, verStr2, pver2)
	}
}

func versionEqual(t *testing.T, verStr string, ver GoVersion) {
	t.Helper()
	pver := parseVer(t, verStr)
	if pver != ver {
		t.Fatalf("Version <%s> parsed as %v not equal to %v", verStr, pver, ver)
	}
	t.Logf("version string <%s> → %v", verStr, ver)
}

func TestParseVersionStringAfterOrEqual(t *testing.T) {
	versionAfterOrEqual(t, "go1.4", GoVersion{1, 4, 0, "", ""})
	versionAfterOrEqual(t, "go1.5.0", GoVersion{1, 5, 0, "", ""})
	versionAfterOrEqual(t, "go1.4.2", GoVersion{1, 4, 2, "", ""})
	versionAfterOrEqual(t, "go1.5beta2", GoVersion{1, 5, betaRev(2), "", ""})
	versionAfterOrEqual(t, "go1.5rc2", GoVersion{1, 5, rcRev(2), "", ""})
	versionAfterOrEqual(t, "go1.6.1 (appengine-1.9.37)", GoVersion{1, 6, 1, "", ""})
	versionAfterOrEqual(t, "go1.8.1.typealias", GoVersion{1, 6, 1, "", ""})
	versionAfterOrEqual(t, "go1.8b1", GoVersion{1, 8, 0, "", ""})
	versionAfterOrEqual(t, "go1.16.4b7", GoVersion{1, 16, 4, "", ""})
	ver, ok := Parse("devel +17efbfc Tue Jul 28 17:39:19 2015 +0000 linux/amd64")
	if !ok {
		t.Fatalf("Could not parse devel version string")
	}
	if !ver.IsDevel() {
		t.Fatalf("Devel version string not correctly recognized")
	}

	versionAfterOrEqual2(t, "go1.16", "go1.16b1")
	versionAfterOrEqual2(t, "go1.16", "go1.16rc1")
	versionAfterOrEqual2(t, "go1.16rc1", "go1.16beta1")
	versionAfterOrEqual2(t, "go1.16beta2", "go1.16beta1")
	versionAfterOrEqual2(t, "go1.16rc10", "go1.16rc8")
}

func TestParseVersionStringEqual(t *testing.T) {
	versionEqual(t, "go1.4", GoVersion{1, 4, 0, "", ""})
	versionEqual(t, "go1.5.0", GoVersion{1, 5, 0, "", ""})
	versionEqual(t, "go1.4.2", GoVersion{1, 4, 2, "", ""})
	versionEqual(t, "go1.5beta2", GoVersion{1, 5, betaRev(2), "", ""})
	versionEqual(t, "go1.5rc2", GoVersion{1, 5, rcRev(2), "", ""})
	versionEqual(t, "go1.6.1 (appengine-1.9.37)", GoVersion{1, 6, 1, "", ""})
	versionEqual(t, "go1.8.1.typealias", GoVersion{1, 8, 1, "typealias", ""})
	versionEqual(t, "go1.8b1", GoVersion{1, 8, 0, "", ""})
	versionEqual(t, "go1.16.4b7", GoVersion{1, 16, 4, "", ""})
	versionEqual(t, "go1.21.1-something", GoVersion{1, 21, 1, "", "something"})
	versionEqual(t, "devel +17efbfc Tue Jul 28 17:39:19 2015 +0000 linux/amd64", GoVersion{Major: -1})
}

func TestRoundtrip(t *testing.T) {
	for _, verStr := range []string{
		"go1.4",
		"go1.4.2",
		"go1.5beta2",
		"go1.5rc2",
		"go1.8.1.typealias",
		"go1.21.1-something",
		"go1.21.0",
	} {
		pver := parseVer(t, verStr)
		if pver.String() != verStr {
			t.Fatalf("roundtrip mismatch <%s> -> %#v -> <%s>", verStr, pver, pver.String())
		}
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
