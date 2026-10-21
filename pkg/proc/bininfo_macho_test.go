package proc

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/go-delve/delve/pkg/goversion"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestLoadingSplitDwarfMacho(t *testing.T) {
	for _, compression := range []string{"true", "false"} {
		t.Run("compressdwarf="+compression, func(t *testing.T) {
			referencePath := buildMachoFixture(t, "-compressdwarf="+compression)
			stripped := buildMachoFixture(t, "-w")
			writeMachoDSYM(t, stripped, machoDSYMFixture(t, referencePath))
			reference := loadMachoBinaryInfo(t, referencePath)
			split := loadMachoBinaryInfo(t, stripped)
			if split.Images[0].Stripped() {
				t.Fatal("UUID-matched dSYM was not loaded")
			}
			if len(split.Functions) != len(reference.Functions) {
				t.Fatalf("function count: got %d, want %d", len(split.Functions), len(reference.Functions))
			}
			for i, want := range reference.Functions {
				got := split.Functions[i]
				if got.Name != want.Name || got.Entry != want.Entry || got.End != want.End {
					t.Fatalf("function %d: got %s [%#x, %#x), want %s [%#x, %#x)", i, got.Name, got.Entry, got.End, want.Name, want.Entry, want.End)
				}
				file, line, _ := split.PCToLine(got.Entry)
				wantFile, wantLine, _ := reference.PCToLine(want.Entry)
				if file != wantFile || line != wantLine {
					t.Fatalf("%s source: got %s:%d, want %s:%d", got.Name, file, line, wantFile, wantLine)
				}
			}
			if !reflect.DeepEqual(split.frameEntries, reference.frameEntries) {
				t.Error("frame entries differ from embedded DWARF")
			}
			got, want := split.Images[0], reference.Images[0]
			if !reflect.DeepEqual(got.loclist2, want.loclist2) || !reflect.DeepEqual(got.loclist5, want.loclist5) || !reflect.DeepEqual(got.debugAddr, want.debugAddr) || !reflect.DeepEqual(got.debugLineStr, want.debugLineStr) {
				t.Error("auxiliary DWARF sections differ from embedded DWARF")
			}
		})
	}
}

func TestLoadingMachoRejectsInvalidDSYM(t *testing.T) {
	// Bad bundles must leave the executable's stripped-binary fallback intact,
	// rather than supplying unrelated types or source addresses.
	stripped := buildMachoFixture(t, "-w")
	reference := buildMachoFixture(t, "")
	for _, name := range []string{"missing", "malformed", "mismatched UUID", "missing dSYM UUID", "missing executable UUID"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "split")
			exeData, err := os.ReadFile(stripped)
			assertNoError(err, t, "ReadFile")
			dsymData := machoDSYMFixture(t, reference)
			switch name {
			case "malformed":
				dsymData = []byte("not a Mach-O file")
			case "mismatched UUID":
				mutateMachoUUID(t, dsymData, false)
			case "missing dSYM UUID":
				mutateMachoUUID(t, dsymData, true)
			case "missing executable UUID":
				mutateMachoUUID(t, exeData, true)
			}
			assertNoError(os.WriteFile(path, exeData, 0600), t, "WriteFile")
			if name != "missing" {
				writeMachoDSYM(t, path, dsymData)
			}
			checkMachoStrippedFallback(t, path)
		})
	}
}

func TestLoadingMachoIncompleteDSYMFallsBack(t *testing.T) {
	stripped := buildMachoFixture(t, "-w")
	reference := buildMachoFixture(t, "-compressdwarf=false")
	data := machoDSYMFixture(t, reference)
	// Rename the section in its Mach-O header, preserving valid debug_info
	// so DWARF() still succeeds. Delve must validate debug_line as well.
	offset := bytes.Index(data, []byte("__debug_line\x00"))
	if offset < 0 {
		t.Fatal("fixture has no __debug_line section")
	}
	copy(data[offset:], "__absent_line")
	writeMachoDSYM(t, stripped, data)
	checkMachoStrippedFallback(t, stripped)
}

func TestLoadingMachoDSYMTakesPrecedence(t *testing.T) {
	for _, compression := range []string{"true", "false"} {
		t.Run("compressdwarf="+compression, func(t *testing.T) {
			path := buildMachoFixture(t, "-compressdwarf="+compression)
			writeMachoDSYM(t, path, machoDSYMFixture(t, path))
			bi := loadMachoBinaryInfo(t, path)
			if bi.Images[0].Stripped() || bi.Images[0].sepDebugCloser == nil {
				t.Fatal("UUID-matched dSYM did not take precedence over embedded DWARF")
			}
		})
	}
}

// buildMachoFixture builds Darwin fixtures even on non-Darwin hosts and Go
// versions predating split DWARF. Tests construct dSYMs from embedded DWARF.
func buildMachoFixture(t *testing.T, ldflags string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "math")
	// On Darwin, -B sets LC_UUID. Use the same fixed UUID for both builds
	// so their identities match despite the different DWARF flags.
	ldflagsBase := "-B 0x00112233445566778899aabbccddeeff"
	// Go 1.28 splits DWARF by default; keep it embedded in the reference
	// so the test can construct the separate debug file itself.
	if goversion.VersionAfterOrEqual(runtime.Version(), 1, 28) {
		ldflagsBase += " -splitdwarf=false"
	}
	cmd := exec.Command("go", "build", "-gcflags=all=-N -l", "-ldflags="+ldflagsBase+" "+ldflags, "-o", path, filepath.Join(protest.FindFixturesDir(), "math.go"))
	cmd.Env = append(os.Environ(), "GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	return path
}

func loadMachoBinaryInfo(t *testing.T, path string) *BinaryInfo {
	t.Helper()
	bi := NewBinaryInfo("darwin", "arm64", false)
	t.Cleanup(func() { assertNoError(bi.Close(), t, "Close") })
	assertNoError(bi.LoadBinaryInfo(path, 0, nil), t, "LoadBinaryInfo")
	return bi
}

func machoDSYMFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	assertNoError(err, t, "ReadFile")
	f, err := macho.NewFile(bytes.NewReader(data))
	assertNoError(err, t, "macho.NewFile")
	defer f.Close()
	// MH_DSYM has the same section layout as other Mach-O files.
	f.ByteOrder.PutUint32(data[12:16], 0xa) // MH_DSYM
	return data
}

func writeMachoDSYM(t *testing.T, path string, data []byte) {
	t.Helper()
	dsymPath := filepath.Join(path+".dSYM", "Contents", "Resources", "DWARF", filepath.Base(path))
	assertNoError(os.MkdirAll(filepath.Dir(dsymPath), 0755), t, "MkdirAll")
	assertNoError(os.WriteFile(dsymPath, data, 0600), t, "WriteFile")
}

func checkMachoStrippedFallback(t *testing.T, path string) {
	t.Helper()
	bi := loadMachoBinaryInfo(t, path)
	if !bi.Images[0].Stripped() || bi.Images[0].sepDebugCloser != nil {
		t.Fatal("loaded an invalid dSYM")
	}
	if len(bi.Functions) == 0 {
		t.Fatal("lost executable's Go symbol table")
	}
}

// mutateMachoUUID changes either the UUID payload or its command number.
func mutateMachoUUID(t *testing.T, data []byte, remove bool) {
	t.Helper()
	f, err := macho.NewFile(bytes.NewReader(data))
	assertNoError(err, t, "macho.NewFile")
	defer f.Close()
	offset := 32 // mach_header_64
	for _, load := range f.Loads {
		raw := load.Raw()
		if f.ByteOrder.Uint32(raw[:4]) == 0x1b {
			if remove {
				f.ByteOrder.PutUint32(data[offset:offset+4], 0xffff) // unknown load command
			} else {
				data[offset+8] ^= 0xff
			}
			return
		}
		offset += len(raw)
	}
	t.Fatal("fixture has no LC_UUID")
}
