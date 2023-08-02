package locspec

import (
	"fmt"
	"go/constant"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/service/api"
)

const maxFindLocationCandidates = 5

// LocationSpec is an interface that represents a parsed location spec string.
type LocationSpec interface {
	// Find returns all locations that match the location spec.
	Find(t *proc.Target, processArgs []string, scope *proc.EvalScope, locStr string, includeNonExecutableLines bool, substitutePathRules [][2]string) ([]api.Location, string, error)
}

// NormalLocationSpec represents a basic location spec.
// This can be a file:line or func:line.
type NormalLocationSpec struct {
	Base       string
	FuncBase   *FuncLocationSpec
	LineOffset int
}

// RegexLocationSpec represents a regular expression
// location expression such as /^myfunc$/.
type RegexLocationSpec struct {
	FuncRegex string
}

// AddrLocationSpec represents an address when used
// as a location spec.
type AddrLocationSpec struct {
	AddrExpr string
}

// OffsetLocationSpec represents a location spec that
// is an offset of the current location (file:line).
type OffsetLocationSpec struct {
	Offset int
}

// LineLocationSpec represents a line number in the current file.
type LineLocationSpec struct {
	Line int
}

// FuncLocationSpec represents a function in the target program.
type FuncLocationSpec struct {
	PackageName           string
	AbsolutePackage       bool
	ReceiverName          string
	PackageOrReceiverName string
	BaseName              string
}

// Parse will turn locStr into a parsed LocationSpec.
func Parse(locStr string) (LocationSpec, error) {
	rest := locStr

	malformed := func(reason string) error {
		//lint:ignore ST1005 backwards compatibility
		return fmt.Errorf("Malformed breakpoint location %q at %d: %s", locStr, len(locStr)-len(rest), reason)
	}

	if len(rest) == 0 {
		return nil, malformed("empty string")
	}

	switch rest[0] {
	case '+', '-':
		offset, err := strconv.Atoi(rest)
		if err != nil {
			return nil, malformed(err.Error())
		}
		return &OffsetLocationSpec{offset}, nil

	case '/':
		if rest[len(rest)-1] == '/' {
			rx, rest := readRegex(rest[1:])
			if len(rest) == 0 {
				return nil, malformed("non-terminated regular expression")
			}
			if len(rest) > 1 {
				return nil, malformed("no line offset can be specified for regular expression locations")
			}
			return &RegexLocationSpec{rx}, nil
		} else {
			return parseLocationSpecDefault(locStr, rest)
		}

	case '*':
		return &AddrLocationSpec{AddrExpr: rest[1:]}, nil

	default:
		return parseLocationSpecDefault(locStr, rest)
	}
}

func parseLocationSpecDefault(locStr, rest string) (LocationSpec, error) {
	malformed := func(reason string) error {
		//lint:ignore ST1005 backwards compatibility
		return fmt.Errorf("Malformed breakpoint location %q at %d: %s", locStr, len(locStr)-len(rest), reason)
	}

	v := strings.Split(rest, ":")
	if len(v) > 2 {
		// On Windows, path may contain ":", so split only on last ":"
		v = []string{strings.Join(v[0:len(v)-1], ":"), v[len(v)-1]}
	}

	if len(v) == 1 {
		n, err := strconv.ParseInt(v[0], 0, 64)
		if err == nil {
			return &LineLocationSpec{int(n)}, nil
		}
	}

	spec := &NormalLocationSpec{}

	spec.Base = v[0]
	spec.FuncBase = parseFuncLocationSpec(spec.Base)

	if len(v) < 2 {
		spec.LineOffset = -1
		return spec, nil
	}

	rest = v[1]

	var err error
	spec.LineOffset, err = strconv.Atoi(rest)
	if err != nil || spec.LineOffset < 0 {
		return nil, malformed("line offset negative or not a number")
	}

	return spec, nil
}

func readRegex(in string) (rx string, rest string) {
	out := make([]rune, 0, len(in))
	escaped := false
	for i, ch := range in {
		if escaped {
			if ch == '/' {
				out = append(out, '/')
			} else {
				out = append(out, '\\', ch)
			}
			escaped = false
		} else {
			switch ch {
			case '\\':
				escaped = true
			case '/':
				return string(out), in[i:]
			default:
				out = append(out, ch)
			}
		}
	}
	return string(out), ""
}

func parseFuncLocationSpec(in string) *FuncLocationSpec {
	var v []string
	pathend := strings.LastIndex(in, "/")
	if pathend < 0 {
		v = strings.Split(in, ".")
	} else {
		v = strings.Split(in[pathend:], ".")
		if len(v) > 0 {
			v[0] = in[:pathend] + v[0]
		}
	}

	var spec FuncLocationSpec
	switch len(v) {
	case 1:
		spec.BaseName = v[0]

	case 2:
		spec.BaseName = v[1]
		r := stripReceiverDecoration(v[0])
		if r != v[0] {
			spec.ReceiverName = r
		} else if strings.Contains(r, "/") {
			spec.PackageName = r
		} else {
			spec.PackageOrReceiverName = r
		}

	case 3:
		spec.BaseName = v[2]
		spec.ReceiverName = stripReceiverDecoration(v[1])
		spec.PackageName = v[0]

	default:
		return nil
	}

	if strings.HasPrefix(spec.PackageName, "/") {
		spec.PackageName = spec.PackageName[1:]
		spec.AbsolutePackage = true
	}

	if strings.Contains(spec.BaseName, "/") || strings.Contains(spec.ReceiverName, "/") {
		return nil
	}

	return &spec
}

func stripReceiverDecoration(in string) string {
	if len(in) < 3 {
		return in
	}
	if (in[0] != '(') || (in[1] != '*') || (in[len(in)-1] != ')') {
		return in
	}

	return in[2 : len(in)-1]
}

// Match will return whether the provided function matches the location spec.
func (spec *FuncLocationSpec) Match(sym *proc.Function, packageMap map[string][]string) bool {
	if spec.BaseName != sym.BaseName() {
		return false
	}

	recv := stripReceiverDecoration(sym.ReceiverName())
	if spec.ReceiverName != "" && spec.ReceiverName != recv {
		return false
	}
	if spec.PackageName != "" {
		if spec.AbsolutePackage {
			if spec.PackageName != sym.PackageName() {
				return false
			}
		} else {
			if !packageMatch(spec.PackageName, sym.PackageName(), packageMap) {
				return false
			}
		}
	}
	if spec.PackageOrReceiverName != "" && !packageMatch(spec.PackageOrReceiverName, sym.PackageName(), packageMap) && spec.PackageOrReceiverName != recv {
		return false
	}
	return true
}

func packageMatch(specPkg, symPkg string, packageMap map[string][]string) bool {
	for _, pkg := range packageMap[specPkg] {
		if partialPackageMatch(pkg, symPkg) {
			return true
		}
	}
	return partialPackageMatch(specPkg, symPkg)
}

// Find will search all functions in the target program and filter them via the
// regex location spec. Only functions matching the regex will be returned.
func (loc *RegexLocationSpec) Find(t *proc.Target, _ []string, scope *proc.EvalScope, locStr string, includeNonExecutableLines bool, _ [][2]string) ([]api.Location, string, error) {
	if scope == nil {
		//TODO(aarzilli): this needs only the list of function we should make it work
		return nil, "", fmt.Errorf("could not determine location (scope is nil)")
	}
	funcs := scope.BinInfo.Functions
	matches, err := regexFilterFuncs(loc.FuncRegex, funcs)
	if err != nil {
		return nil, "", err
	}
	r := make([]api.Location, 0, len(matches))
	for i := range matches {
		addrs, _ := proc.FindFunctionLocation(t, matches[i], 0)
		if len(addrs) > 0 {
			r = append(r, addressesToLocation(addrs))
		}
	}
	return r, "", nil
}

// Find returns the locations specified via the address location spec.
func (loc *AddrLocationSpec) Find(t *proc.Target, _ []string, scope *proc.EvalScope, locStr string, includeNonExecutableLines bool, _ [][2]string) ([]api.Location, string, error) {
	if scope == nil {
		addr, err := strconv.ParseInt(loc.AddrExpr, 0, 64)
		if err != nil {
			return nil, "", fmt.Errorf("could not determine current location (scope is nil)")
		}
		return []api.Location{{PC: uint64(addr)}}, "", nil
	}

	v, err := scope.EvalExpression(loc.AddrExpr, proc.LoadConfig{FollowPointers: true})
	if err != nil {
		return nil, "", err
	}
	if v.Unreadable != nil {
		return nil, "", v.Unreadable
	}
	switch v.Kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		addr, _ := constant.Uint64Val(v.Value)
		return []api.Location{{PC: addr}}, "", nil
	case reflect.Func:
		fn := scope.BinInfo.PCToFunc(uint64(v.Base))
		pc, err := proc.FirstPCAfterPrologue(t, fn, false)
		if err != nil {
			return nil, "", err
		}
		return []api.Location{{PC: pc}}, v.Name, nil
	default:
		return nil, "", fmt.Errorf("wrong expression kind: %v", v.Kind)
	}
}

// FileMatch is true if the path matches the location spec.
func (loc *NormalLocationSpec) FileMatch(path string) bool {
	return partialPathMatch(loc.Base, path)
}

func tryMatchRelativePathByProc(expr, debugname, file string) bool {
	return len(expr) > 0 && expr[0] == '.' && file == path.Join(path.Dir(debugname), expr)
}

func partialPathMatch(expr, path string) bool {
	if runtime.GOOS == "windows" {
		// Accept `expr` which is case-insensitive and slash-insensitive match to `path`
		expr = strings.ToLower(filepath.ToSlash(expr))
		path = strings.ToLower(filepath.ToSlash(path))
	}
	return partialPackageMatch(expr, path)
}

func partialPackageMatch(expr, path string) bool {
	if len(expr) < len(path)-1 {
		return strings.HasSuffix(path, expr) && (path[len(path)-len(expr)-1] == '/')
	}
	return expr == path
}

// AmbiguousLocationError is returned when the location spec
// should only return one location but returns multiple instead.
type AmbiguousLocationError struct {
	Location           string
	CandidatesString   []string
	CandidatesLocation []api.Location
}

func (ale AmbiguousLocationError) Error() string {
	var candidates []string
	if ale.CandidatesLocation != nil {
		for i := range ale.CandidatesLocation {
			candidates = append(candidates, ale.CandidatesLocation[i].Function.Name())
		}

	} else {
		candidates = ale.CandidatesString
	}
	return fmt.Sprintf("Location %q ambiguous: %s…", ale.Location, strings.Join(candidates, ", "))
}

// Find will return a list of locations that match the given location spec.
// This matches each other location spec that does not already have its own spec
// implemented (such as regex, or addr).
func (loc *NormalLocationSpec) Find(t *proc.Target, processArgs []string, scope *proc.EvalScope, locStr string, includeNonExecutableLines bool, substitutePathRules [][2]string) ([]api.Location, string, error) {
	limit := maxFindLocationCandidates
	var candidateFiles []string
	for _, sourceFile := range t.BinInfo().Sources {
		substFile := sourceFile
		if len(substitutePathRules) > 0 {
			substFile = SubstitutePath(sourceFile, substitutePathRules)
		}
		if loc.FileMatch(substFile) || (len(processArgs) >= 1 && tryMatchRelativePathByProc(loc.Base, processArgs[0], substFile)) {
			candidateFiles = append(candidateFiles, sourceFile)
			if len(candidateFiles) >= limit {
				break
			}
		}
	}

	limit -= len(candidateFiles)

	var candidateFuncs []string
	if loc.FuncBase != nil && limit > 0 {
		candidateFuncs = loc.findFuncCandidates(t.BinInfo(), limit)
	}

	if matching := len(candidateFiles) + len(candidateFuncs); matching == 0 {
		if scope == nil {
			return nil, "", fmt.Errorf("location %q not found", locStr)
		}
		// if no result was found this locations string could be an
		// expression that the user forgot to prefix with '*', try treating it as
		// such.
		addrSpec := &AddrLocationSpec{AddrExpr: locStr}
		locs, subst, err := addrSpec.Find(t, processArgs, scope, locStr, includeNonExecutableLines, nil)
		if err != nil {
			return nil, "", fmt.Errorf("location %q not found", locStr)
		}
		return locs, subst, nil
	} else if matching > 1 {
		return nil, "", AmbiguousLocationError{Location: locStr, CandidatesString: append(candidateFiles, candidateFuncs...)}
	}

	// len(candidateFiles) + len(candidateFuncs) == 1
	var addrs []uint64
	var err error
	if len(candidateFiles) == 1 {
		if loc.LineOffset < 0 {
			//lint:ignore ST1005 backwards compatibility
			return nil, "", fmt.Errorf("Malformed breakpoint location, no line offset specified")
		}
		addrs, err = proc.FindFileLocation(t, candidateFiles[0], loc.LineOffset)
		if includeNonExecutableLines {
			if _, isCouldNotFindLine := err.(*proc.ErrCouldNotFindLine); isCouldNotFindLine {
				return []api.Location{{File: candidateFiles[0], Line: loc.LineOffset}}, "", nil
			}
		}
	} else { // len(candidateFuncs) == 1
		addrs, err = proc.FindFunctionLocation(t, candidateFuncs[0], loc.LineOffset)
	}

	if err != nil {
		return nil, "", err
	}
	return []api.Location{addressesToLocation(addrs)}, "", nil
}

func (loc *NormalLocationSpec) findFuncCandidates(bi *proc.BinaryInfo, limit int) []string {
	candidateFuncs := map[string]struct{}{}
	// See if it matches generic functions first
	for fname := range bi.LookupGenericFunc() {
		if len(candidateFuncs) >= limit {
			break
		}
		if !loc.FuncBase.Match(&proc.Function{Name: fname}, bi.PackageMap) {
			continue
		}
		if loc.Base == fname {
			return []string{fname}
		}
		candidateFuncs[fname] = struct{}{}
	}
	for _, fns := range bi.LookupFunc() {
		f := fns[0]
		if len(candidateFuncs) >= limit {
			break
		}
		if !loc.FuncBase.Match(f, bi.PackageMap) {
			continue
		}
		if loc.Base == f.Name {
			// if an exact match for the function name is found use it
			return []string{f.Name}
		}
		// If f is an instantiation of a generic function see if we should add its generic version instead.
		if gn := f.NameWithoutTypeParams(); gn != "" {
			if _, alreadyAdded := candidateFuncs[gn]; !alreadyAdded {
				candidateFuncs[f.Name] = struct{}{}
			}
		} else {
			candidateFuncs[f.Name] = struct{}{}
		}
	}
	// convert candidateFuncs map into an array of its keys
	r := make([]string, 0, len(candidateFuncs))
	for s := range candidateFuncs {
		r = append(r, s)
	}
	return r
}

// isAbs returns true if path looks like an absolute path.
func isAbs(path string) bool {
	// Unix-like absolute path
	if strings.HasPrefix(path, "/") {
		return true
	}
	return windowsAbsPath(path)
}

func windowsAbsPath(path string) bool {
	// Windows UNC absolute path
	if strings.HasPrefix(path, `\\`) {
		return true
	}
	// DOS absolute paths
	if len(path) < 3 || path[1] != ':' {
		return false
	}
	return path[2] == '/' || path[2] == '\\'
}

func hasPathSeparatorSuffix(path string) bool {
	return strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\")
}

func hasPathSeparatorPrefix(path string) bool {
	return strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\")
}

func pickSeparator(to string) string {
	var sep byte
	for i := range to {
		if to[i] == '/' || to[i] == '\\' {
			if sep == 0 {
				sep = to[i]
			} else if sep != to[i] {
				return ""
			}
		}
	}
	return string(sep)
}

func joinPath(to, rest string) string {
	sep := pickSeparator(to)

	switch sep {
	case "/":
		rest = strings.ReplaceAll(rest, "\\", sep)
	case "\\":
		rest = strings.ReplaceAll(rest, "/", sep)
	default:
		sep = "/"
	}

	toEndsWithSlash := hasPathSeparatorSuffix(to)
	restStartsWithSlash := hasPathSeparatorPrefix(rest)

	switch {
	case toEndsWithSlash && restStartsWithSlash:
		return to[:len(to)-1] + rest
	case toEndsWithSlash && !restStartsWithSlash:
		return to + rest
	case !toEndsWithSlash && restStartsWithSlash:
		return to + rest
	case !toEndsWithSlash && !restStartsWithSlash:
		fallthrough
	default:
		return to + sep + rest
	}
}

// SubstitutePath applies the specified path substitution rules to path.
func SubstitutePath(path string, rules [][2]string) string {
	// Look for evidence that we are dealing with windows somewhere, if we are use case-insensitive matching
	caseInsensitive := windowsAbsPath(path)
	if !caseInsensitive {
		for i := range rules {
			if windowsAbsPath(rules[i][0]) || windowsAbsPath(rules[i][1]) {
				caseInsensitive = true
				break
			}
		}
	}
	for _, r := range rules {
		from, to := r[0], r[1]

		// if we have an exact match, use it directly.
		if path == from {
			return to
		}

		match := false
		var rest string
		if from == "" {
			match = !isAbs(path)
			rest = path
		} else {
			if caseInsensitive {
				match = strings.HasPrefix(strings.ToLower(path), strings.ToLower(from))
				if match {
					path = strings.ToLower(path)
					from = strings.ToLower(from)
				}
			} else {
				match = strings.HasPrefix(path, from)
			}
			if match {
				// make sure the match ends on something that looks like a path separator boundary
				rest = path[len(from):]
				match = hasPathSeparatorSuffix(from) || hasPathSeparatorPrefix(rest)
			}
		}

		if match {
			if to == "" {
				// make sure we return a relative path, regardless of whether 'from' consumed a final / or not
				if hasPathSeparatorPrefix(rest) {
					return rest[1:]
				}
				return rest
			}

			return joinPath(to, rest)
		}
	}
	return path
}

func addressesToLocation(addrs []uint64) api.Location {
	if len(addrs) == 0 {
		return api.Location{}
	}
	return api.Location{PC: addrs[0], PCs: addrs}
}

// Find returns the location after adding the offset amount to the current line number.
func (loc *OffsetLocationSpec) Find(t *proc.Target, _ []string, scope *proc.EvalScope, _ string, includeNonExecutableLines bool, _ [][2]string) ([]api.Location, string, error) {
	if scope == nil {
		return nil, "", fmt.Errorf("could not determine current location (scope is nil)")
	}
	file, line, fn := scope.BinInfo.PCToLine(scope.PC)
	if loc.Offset == 0 {
		subst := ""
		if fn != nil {
			subst = fmt.Sprintf("%s:%d", file, line)
		}
		return []api.Location{{PC: scope.PC}}, subst, nil
	}
	if fn == nil {
		return nil, "", fmt.Errorf("could not determine current location")
	}
	subst := fmt.Sprintf("%s:%d", file, line+loc.Offset)
	addrs, err := proc.FindFileLocation(t, file, line+loc.Offset)
	if includeNonExecutableLines {
		if _, isCouldNotFindLine := err.(*proc.ErrCouldNotFindLine); isCouldNotFindLine {
			return []api.Location{{File: file, Line: line + loc.Offset}}, subst, nil
		}
	}
	return []api.Location{addressesToLocation(addrs)}, subst, err
}

// Find will return the location at the given line in the current file.
func (loc *LineLocationSpec) Find(t *proc.Target, _ []string, scope *proc.EvalScope, _ string, includeNonExecutableLines bool, _ [][2]string) ([]api.Location, string, error) {
	if scope == nil {
		return nil, "", fmt.Errorf("could not determine current location (scope is nil)")
	}
	file, _, fn := scope.BinInfo.PCToLine(scope.PC)
	if fn == nil {
		return nil, "", fmt.Errorf("could not determine current location")
	}
	subst := fmt.Sprintf("%s:%d", file, loc.Line)
	addrs, err := proc.FindFileLocation(t, file, loc.Line)
	if includeNonExecutableLines {
		if _, isCouldNotFindLine := err.(*proc.ErrCouldNotFindLine); isCouldNotFindLine {
			return []api.Location{{File: file, Line: loc.Line}}, subst, nil
		}
	}
	return []api.Location{addressesToLocation(addrs)}, subst, err
}

func regexFilterFuncs(filter string, allFuncs []proc.Function) ([]string, error) {
	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	funcs := []string{}
	for _, f := range allFuncs {
		if regex.MatchString(f.Name) {
			funcs = append(funcs, f.Name)
		}
	}
	return funcs, nil
}
