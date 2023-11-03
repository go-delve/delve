// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gosym

import (
	"encoding/binary"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	funcSymNameGo119Lower string = "go.func.*"
	funcSymNameGo120      string = "go:func.*"
)

// Additions to the original package from cmd/internal/objabi/funcdata.go
const (
	pcdata_InlTreeIndex = 2
	funcdata_InlTree    = 3
)

var (
	// Regexp for matching go tags. The groups are:
	// 1  the major.minor version
	// 2  the patch version, or empty if none
	// 3  the entire prerelease, if present
	// 4  the prerelease type ("beta" or "rc")
	// 5  the prerelease number
	tagRegexp = regexp.MustCompile(`^go(\d+\.\d+)(\.\d+|)((beta|rc|-pre)(\d+))?$`)
)

// parsed returns the parsed form of a semantic version string.
type parsed struct {
	major      string
	minor      string
	patch      string
	short      string
	prerelease string
	build      string
}

func parsePrerelease(v string) (t, rest string, ok bool) {
	// "A pre-release version MAY be denoted by appending a hyphen and
	// a series of dot separated identifiers immediately following the patch version.
	// Identifiers MUST comprise only ASCII alphanumerics and hyphen [0-9A-Za-z-].
	// Identifiers MUST NOT be empty. Numeric identifiers MUST NOT include leading zeroes."
	if v == "" || v[0] != '-' {
		return
	}
	i := 1
	start := 1
	for i < len(v) && v[i] != '+' {
		if !isIdentChar(v[i]) && v[i] != '.' {
			return
		}
		if v[i] == '.' {
			if start == i || isBadNum(v[start:i]) {
				return
			}
			start = i + 1
		}
		i++
	}
	if start == i || isBadNum(v[start:i]) {
		return
	}
	return v[:i], v[i:], true
}

// GoTagToSemver is a modified copy of pkgsite/internal/stdlib:VersionForTag.
func GoTagToSemver(tag string) string {
	if tag == "" {
		return ""
	}
	tag = strings.Fields(tag)[0]
	// Special cases for go1.
	if tag == "go1" {
		return "v1.0.0"
	}
	if tag == "go1.0" {
		return ""
	}
	m := tagRegexp.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	version := "v" + m[1]
	if m[2] != "" {
		version += m[2]
	} else {
		version += ".0"
	}
	if m[3] != "" {
		if !strings.HasPrefix(m[4], "-") {
			version += "-"
		}
		version += m[4] + "." + m[5]
	}
	return version
}

func parseBuild(v string) (t, rest string, ok bool) {
	if v == "" || v[0] != '+' {
		return
	}
	i := 1
	start := 1
	for i < len(v) {
		if !isIdentChar(v[i]) && v[i] != '.' {
			return
		}
		if v[i] == '.' {
			if start == i {
				return
			}
			start = i + 1
		}
		i++
	}
	if start == i {
		return
	}
	return v[:i], v[i:], true
}
func parseInt(v string) (t, rest string, ok bool) {
	if v == "" {
		return
	}
	if v[0] < '0' || '9' < v[0] {
		return
	}
	i := 1
	for i < len(v) && '0' <= v[i] && v[i] <= '9' {
		i++
	}
	if v[0] == '0' && i != 1 {
		return
	}
	return v[:i], v[i:], true
}
func isIdentChar(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-'
}

func parse(v string) (p parsed, ok bool) {
	if v == "" || v[0] != 'v' {
		return
	}
	p.major, v, ok = parseInt(v[1:])
	if !ok {
		return
	}
	if v == "" {
		p.minor = "0"
		p.patch = "0"
		p.short = ".0.0"
		return
	}
	if v[0] != '.' {
		ok = false
		return
	}
	p.minor, v, ok = parseInt(v[1:])
	if !ok {
		return
	}
	if v == "" {
		p.patch = "0"
		p.short = ".0"
		return
	}
	if v[0] != '.' {
		ok = false
		return
	}
	p.patch, v, ok = parseInt(v[1:])
	if !ok {
		return
	}
	if len(v) > 0 && v[0] == '-' {
		p.prerelease, v, ok = parsePrerelease(v)
		if !ok {
			return
		}
	}
	if len(v) > 0 && v[0] == '+' {
		p.build, v, ok = parseBuild(v)
		if !ok {
			return
		}
	}
	if v != "" {
		ok = false
		return
	}
	ok = true
	return
}

func isBadNum(v string) bool {
	i := 0
	for i < len(v) && '0' <= v[i] && v[i] <= '9' {
		i++
	}
	return i == len(v) && i > 1 && v[0] == '0'
}
func nextIdent(x string) (dx, rest string) {
	i := 0
	for i < len(x) && x[i] != '.' {
		i++
	}
	return x[:i], x[i:]
}
func isNum(v string) bool {
	i := 0
	for i < len(v) && '0' <= v[i] && v[i] <= '9' {
		i++
	}
	return i == len(v)
}

// MajorMinor returns the major.minor version prefix of the semantic version v.
// For example, MajorMinor("v2.1.0") == "v2.1".
// If v is an invalid semantic version string, MajorMinor returns the empty string.
func MajorMinor(v string) string {
	pv, ok := parse(v)
	if !ok {
		return ""
	}
	i := 1 + len(pv.major)
	if j := i + 1 + len(pv.minor); j <= len(v) && v[i] == '.' && v[i+1:j] == pv.minor {
		return v[:j]
	}
	return v[:i] + "." + pv.minor
}

func compareInt(x, y string) int {
	if x == y {
		return 0
	}
	if len(x) < len(y) {
		return -1
	}
	if len(x) > len(y) {
		return +1
	}
	if x < y {
		return -1
	} else {
		return +1
	}
}
func comparePrerelease(x, y string) int {
	// "When major, minor, and patch are equal, a pre-release version has
	// lower precedence than a normal version.
	// Example: 1.0.0-alpha < 1.0.0.
	// Precedence for two pre-release versions with the same major, minor,
	// and patch version MUST be determined by comparing each dot separated
	// identifier from left to right until a difference is found as follows:
	// identifiers consisting of only digits are compared numerically and
	// identifiers with letters or hyphens are compared lexically in ASCII
	// sort order. Numeric identifiers always have lower precedence than
	// non-numeric identifiers. A larger set of pre-release fields has a
	// higher precedence than a smaller set, if all of the preceding
	// identifiers are equal.
	// Example: 1.0.0-alpha < 1.0.0-alpha.1 < 1.0.0-alpha.beta <
	// 1.0.0-beta < 1.0.0-beta.2 < 1.0.0-beta.11 < 1.0.0-rc.1 < 1.0.0."
	if x == y {
		return 0
	}
	if x == "" {
		return +1
	}
	if y == "" {
		return -1
	}
	for x != "" && y != "" {
		x = x[1:] // skip - or .
		y = y[1:] // skip - or .
		var dx, dy string
		dx, x = nextIdent(x)
		dy, y = nextIdent(y)
		if dx != dy {
			ix := isNum(dx)
			iy := isNum(dy)
			if ix != iy {
				if ix {
					return -1
				} else {
					return +1
				}
			}
			if ix {
				if len(dx) < len(dy) {
					return -1
				}
				if len(dx) > len(dy) {
					return +1
				}
			}
			if dx < dy {
				return -1
			} else {
				return +1
			}
		}
	}
	if x == "" {
		return -1
	} else {
		return +1
	}
}

// Compare returns an integer comparing two versions according to
// semantic version precedence.
// The result will be 0 if v == w, -1 if v < w, or +1 if v > w.
//
// An invalid semantic version string is considered less than a valid one.
// All invalid semantic version strings compare equal to each other.
func Compare(v, w string) int {
	pv, ok1 := parse(v)
	pw, ok2 := parse(w)
	if !ok1 && !ok2 {
		return 0
	}
	if !ok1 {
		return -1
	}
	if !ok2 {
		return +1
	}
	if c := compareInt(pv.major, pw.major); c != 0 {
		return c
	}
	if c := compareInt(pv.minor, pw.minor); c != 0 {
		return c
	}
	if c := compareInt(pv.patch, pw.patch); c != 0 {
		return c
	}
	return comparePrerelease(pv.prerelease, pw.prerelease)
}

// InlineTree returns the inline tree for Func f as a sequence of InlinedCalls.
// goFuncValue is the value of the gosym.FuncSymName symbol.
// baseAddr is the address of the memory region (ELF Prog) containing goFuncValue.
// progReader is a ReaderAt positioned at the start of that region.
func (t *LineTable) InlineTree(f *Func, goFuncValue, baseAddr uint64, progReader io.ReaderAt) ([]InlinedCall, error) {
	if f.inlineTreeCount == 0 {
		return nil, nil
	}
	if f.inlineTreeOffset == ^uint32(0) {
		return nil, nil
	}
	var offset int64
	if t.version >= ver118 {
		offset = int64(goFuncValue - baseAddr + uint64(f.inlineTreeOffset))
	} else {
		offset = int64(uint64(f.inlineTreeOffset) - baseAddr)
	}
	r := io.NewSectionReader(progReader, offset, 1<<32) // pick a size larger than we need
	var ics []InlinedCall
	for i := 0; i < f.inlineTreeCount; i++ {
		if t.version >= ver120 {
			var ric rawInlinedCall120
			if err := binary.Read(r, t.binary, &ric); err != nil {
				return nil, fmt.Errorf("error reading into rawInlinedCall: %#v", err)
			}
			ics = append(ics, InlinedCall{
				FuncID:   ric.FuncID,
				Name:     t.funcName(uint32(ric.NameOff)),
				ParentPC: ric.ParentPC,
			})
		} else {
			var ric rawInlinedCall112
			if err := binary.Read(r, t.binary, &ric); err != nil {
				return nil, err
			}
			ics = append(ics, InlinedCall{
				FuncID:   ric.FuncID,
				Name:     t.funcName(uint32(ric.Func_)),
				ParentPC: ric.ParentPC,
			})
		}
	}
	return ics, nil
}

// FuncSymName returns symbol name for Go functions used in binaries
// based on Go version. Supported Go versions are 1.18 and greater.
// If the go version is unreadable it assumes that it is a newer version
// and returns the symbol name for go version 1.20 or greater.
func FuncSymName(goVersion string) string {
	// Support devel goX.Y...
	v := strings.TrimPrefix(goVersion, "devel ")
	v = GoTagToSemver(v)
	mm := MajorMinor(v)
	if Compare(mm, "v1.20") >= 0 || mm == "" {
		return funcSymNameGo120
	} else if Compare(mm, "v1.18") >= 0 {
		return funcSymNameGo119Lower
	}
	return ""
}

func GetFuncSymName() string {
	return funcSymNameGo120
}

// InlinedCall describes a call to an inlined function.
type InlinedCall struct {
	FuncID   uint8  // type of the called function
	Name     string // name of called function
	ParentPC int32  // position of an instruction whose source position is the call site (offset from entry)
}

// rawInlinedCall112 is the encoding of entries in the FUNCDATA_InlTree table
// from Go 1.12 through 1.19. It is equivalent to runtime.inlinedCall.
type rawInlinedCall112 struct {
	Parent   int16 // index of parent in the inltree, or < 0
	FuncID   uint8 // type of the called function
	_        byte
	File     int32 // perCU file index for inlined call. See cmd/link:pcln.go
	Line     int32 // line number of the call site
	Func_    int32 // offset into pclntab for name of called function
	ParentPC int32 // position of an instruction whose source position is the call site (offset from entry)
}

// rawInlinedCall120 is the encoding of entries in the FUNCDATA_InlTree table
// from Go 1.20. It is equivalent to runtime.inlinedCall.
type rawInlinedCall120 struct {
	FuncID    uint8 // type of the called function
	_         [3]byte
	NameOff   int32 // offset into pclntab for name of called function
	ParentPC  int32 // position of an instruction whose source position is the call site (offset from entry)
	StartLine int32 // line number of start of function (func keyword/TEXT directive)
}

func (f funcData) npcdata() uint32 { return f.field(7) }
func (f funcData) nfuncdata(numFuncFields uint32) uint32 {
	return uint32(f.data[f.fieldOffset(numFuncFields-1)+3])
}

func (f funcData) funcdataOffset(i uint8, numFuncFields uint32) uint32 {
	if uint32(i) >= f.nfuncdata(numFuncFields) {
		return ^uint32(0)
	}
	var off uint32
	if f.t.version >= ver118 {
		off = f.fieldOffset(numFuncFields) + // skip fixed part of _func
			f.npcdata()*4 + // skip pcdata
			uint32(i)*4 // index of i'th FUNCDATA
	} else {
		off = f.fieldOffset(numFuncFields) + // skip fixed part of _func
			f.npcdata()*4
		off += uint32(i) * f.t.ptrsize
	}
	return f.t.binary.Uint32(f.data[off:])
}

func (f funcData) fieldOffset(n uint32) uint32 {
	// In Go 1.18, the first field of _func changed
	// from a uintptr entry PC to a uint32 entry offset.
	sz0 := f.t.ptrsize
	if f.t.version >= ver118 {
		sz0 = 4
	}
	return sz0 + (n-1)*4 // subsequent fields are 4 bytes each
}

func (f funcData) pcdataOffset(i uint8, numFuncFields uint32) uint32 {
	if uint32(i) >= f.npcdata() {
		return ^uint32(0)
	}
	off := f.fieldOffset(numFuncFields) + // skip fixed part of _func
		uint32(i)*4 // index of i'th PCDATA
	return f.t.binary.Uint32(f.data[off:])
}

// maxInlineTreeIndexValue returns the maximum value of the inline tree index
// pc-value table in info. This is the only way to determine how many
// IndexedCalls are in an inline tree, since the data of the tree itself is not
// delimited in any way.
func (t *LineTable) maxInlineTreeIndexValue(info funcData, numFuncFields uint32) int {
	if info.npcdata() <= pcdata_InlTreeIndex {
		return -1
	}
	off := info.pcdataOffset(pcdata_InlTreeIndex, numFuncFields)
	p := t.pctab[off:]
	val := int32(-1)
	max := int32(-1)
	var pc uint64
	for t.step(&p, &pc, &val, pc == 0) {
		if val > max {
			max = val
		}
	}
	return int(max)
}

type inlTree struct {
	inlineTreeOffset uint32 // offset from go.func.* symbol
	inlineTreeCount  int    // number of entries in inline tree
}
