package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/goversion"
	"github.com/spf13/cobra"
)

const DelveMainPackagePath = "github.com/go-delve/delve/cmd/dlv"

var Verbose bool
var NOTimeout bool
var TestIncludePIE bool
var TestSet, TestRegex, TestBackend, TestBuildMode string

func NewMakeCommands() *cobra.Command {
	RootCommand := &cobra.Command{
		Use:   "make.go",
		Short: "make script for delve.",
	}

	RootCommand.AddCommand(&cobra.Command{
		Use:   "check-cert",
		Short: "Check certificate for macOS.",
		Run:   checkCertCmd,
	})

	RootCommand.AddCommand(&cobra.Command{
		Use:   "build",
		Short: "Build delve",
		Run: func(cmd *cobra.Command, args []string) {
			tagFlag := prepareMacnative()
			execute("go", "build", tagFlag, buildFlags(), DelveMainPackagePath)
			if runtime.GOOS == "darwin" && os.Getenv("CERT") != "" && canMacnative() {
				codesign("./dlv")
			}
		},
	})

	RootCommand.AddCommand(&cobra.Command{
		Use:   "install",
		Short: "Installs delve",
		Run: func(cmd *cobra.Command, args []string) {
			tagFlag := prepareMacnative()
			execute("go", "install", tagFlag, buildFlags(), DelveMainPackagePath)
			dlvExecutablePath := installedExecutablePath()
			if runtime.GOOS == "darwin" && os.Getenv("CERT") != "" && canMacnative() {
				codesign(dlvExecutablePath)
			}
			copyDAPBinary(dlvExecutablePath)
		},
	})

	RootCommand.AddCommand(&cobra.Command{
		Use:   "uninstall",
		Short: "Uninstalls delve",
		Run: func(cmd *cobra.Command, args []string) {
			execute("go", "clean", "-i", DelveMainPackagePath)
		},
	})

	test := &cobra.Command{
		Use:   "test",
		Short: "Tests delve",
		Long: `Tests delve.

Use the flags -s, -r and -b to specify which tests to run. Specifying nothing will run all tests relevant for the current environment (see testStandard).
`,
		Run: testCmd,
	}
	test.PersistentFlags().BoolVarP(&Verbose, "verbose", "v", false, "Verbose tests")
	test.PersistentFlags().BoolVarP(&NOTimeout, "timeout", "t", false, "Set infinite timeouts")
	test.PersistentFlags().StringVarP(&TestSet, "test-set", "s", "", `Select the set of tests to run, one of either:
	all		tests all packages
	basic		tests proc, integration and terminal
	integration 	tests github.com/go-delve/delve/service/test
	package-name	test the specified package only
`)
	test.PersistentFlags().StringVarP(&TestRegex, "test-run", "r", "", `Only runs the tests matching the specified regex. This option can only be specified if testset is a single package`)
	test.PersistentFlags().StringVarP(&TestBackend, "test-backend", "b", "", `Runs tests for the specified backend only, one of either:
	default		the default backend
	lldb		lldb backend
	rr		rr backend

This option can only be specified if testset is basic or a single package.`)
	test.PersistentFlags().StringVarP(&TestBuildMode, "test-build-mode", "m", "", `Runs tests compiling with the specified build mode, one of either:
	normal		normal buildmode (default)
	pie		PIE buildmode
	
This option can only be specified if testset is basic or a single package.`)
	test.PersistentFlags().BoolVarP(&TestIncludePIE, "pie", "", true, "Standard testing should include PIE")

	RootCommand.AddCommand(test)

	RootCommand.AddCommand(&cobra.Command{
		Use:   "vendor",
		Short: "vendors dependencies",
		Run: func(cmd *cobra.Command, args []string) {
			execute("go", "mod", "vendor")
		},
	})

	return RootCommand
}

// copyDAPBinary replicates the dlv installation into dlv-dap binary
func copyDAPBinary(dlvExecutablePath string) {
	srcFile, err := os.Open(dlvExecutablePath)
	if err != nil {
		log.Fatal(err)
	}
	defer srcFile.Close()

	dlvDAPExecutablePath := fmt.Sprintf("%s-dap", dlvExecutablePath)

	destFile, err := os.Create(dlvDAPExecutablePath)
	if err != nil {
		log.Fatal(err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		log.Fatal(err)
	}

	fi, _ := srcFile.Stat()
	os.Chmod(dlvDAPExecutablePath, fi.Mode().Perm())

	err = destFile.Sync()
	if err != nil {
		log.Fatal(err)
	}
}

func checkCert() bool {
	// If we're on OSX make sure the proper CERT env var is set.
	if os.Getenv("TRAVIS") == "true" || runtime.GOOS != "darwin" || os.Getenv("CERT") != "" {
		return true
	}

	x := exec.Command("_scripts/gencert.sh")
	x.Stdout = os.Stdout
	x.Stderr = os.Stderr
	x.Env = os.Environ()
	err := x.Run()
	if x.ProcessState != nil && !x.ProcessState.Success() {
		fmt.Printf("An error occurred when generating and installing a new certificate\n")
		return false
	}
	if err != nil {
		fmt.Printf("An error occoured when generating and installing a new certificate: %v\n", err)
		return false
	}
	os.Setenv("CERT", "dlv-cert")
	return true
}

func checkCertCmd(cmd *cobra.Command, args []string) {
	if !checkCert() {
		os.Exit(1)
	}
}

func strflatten(v []interface{}) []string {
	r := []string{}
	for _, s := range v {
		switch s := s.(type) {
		case []string:
			r = append(r, s...)
		case string:
			if s != "" {
				r = append(r, s)
			}
		}
	}
	return r
}

func executeq(cmd string, args ...interface{}) {
	x := exec.Command(cmd, strflatten(args)...)
	x.Stdout = os.Stdout
	x.Stderr = os.Stderr
	x.Env = os.Environ()
	err := x.Run()
	if x.ProcessState != nil && !x.ProcessState.Success() {
		os.Exit(1)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func execute(cmd string, args ...interface{}) {
	fmt.Printf("%s %s\n", cmd, strings.Join(quotemaybe(strflatten(args)), " "))
	executeq(cmd, args...)
}

func quotemaybe(args []string) []string {
	for i := range args {
		if strings.Index(args[i], " ") >= 0 {
			args[i] = fmt.Sprintf("%q", args[i])
		}
	}
	return args
}

func getoutput(cmd string, args ...interface{}) string {
	x := exec.Command(cmd, strflatten(args)...)
	x.Env = os.Environ()
	out, err := x.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing %s %v\n", cmd, args)
		log.Fatal(err)
	}
	if !x.ProcessState.Success() {
		fmt.Fprintf(os.Stderr, "Error executing %s %v\n", cmd, args)
		os.Exit(1)
	}
	return string(out)
}

func codesign(path string) {
	execute("codesign", "-s", os.Getenv("CERT"), path)
}

func installedExecutablePath() string {
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		return filepath.Join(gobin, "dlv")
	}
	gopath := strings.Split(getoutput("go", "env", "GOPATH"), ":")
	return filepath.Join(strings.TrimSpace(gopath[0]), "bin", "dlv")
}

// canMacnative returns true if we can build the native backend for macOS,
// i.e. cgo enabled and the legacy SDK headers:
// https://forums.developer.apple.com/thread/104296
func canMacnative() bool {
	if !(runtime.GOOS == "darwin" && runtime.GOARCH == "amd64") {
		return false
	}
	if strings.TrimSpace(getoutput("go", "env", "CGO_ENABLED")) != "1" {
		return false
	}

	macOSVersion := strings.Split(strings.TrimSpace(getoutput("/usr/bin/sw_vers", "-productVersion")), ".")

	major, err := strconv.ParseInt(macOSVersion[0], 10, 64)
	if err != nil {
		return false
	}
	minor, err := strconv.ParseInt(macOSVersion[1], 10, 64)
	if err != nil {
		return false
	}

	typesHeader := "/usr/include/sys/types.h"
	if major >= 11 || (major == 10 && minor >= 15) {
		typesHeader = "/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/usr/include/sys/types.h"
	}
	_, err = os.Stat(typesHeader)
	if err != nil {
		return false
	}
	return true
}

// prepareMacnative checks if we can build the native backend for macOS and
// if we can checks the certificate and then returns the -tags flag.
func prepareMacnative() string {
	if !canMacnative() {
		return ""
	}
	if !checkCert() {
		return ""
	}
	return "-tags=macnative"
}

func buildFlags() []string {
	buildSHA, err := exec.Command("git", "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		log.Fatal(err)
	}
	ldFlags := "-X main.Build=" + strings.TrimSpace(string(buildSHA))
	if runtime.GOOS == "darwin" {
		ldFlags = "-s " + ldFlags
	}
	return []string{fmt.Sprintf("-ldflags=%s", ldFlags)}
}

func testFlags() []string {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	testFlags := []string{"-count", "1", "-p", "1"}
	if Verbose {
		testFlags = append(testFlags, "-v")
	}
	if NOTimeout {
		testFlags = append(testFlags, "-timeout", "0")
	} else if os.Getenv("TRAVIS") == "true" {
		// Make test timeout shorter than Travis' own timeout so that Go can report which test hangs.
		testFlags = append(testFlags, "-timeout", "9m")
	}
	if len(os.Getenv("TEAMCITY_VERSION")) > 0 {
		testFlags = append(testFlags, "-json")
	}
	if runtime.GOOS == "darwin" {
		testFlags = append(testFlags, "-exec="+wd+"/_scripts/testsign")
	}
	return testFlags
}

func testCmd(cmd *cobra.Command, args []string) {
	checkCertCmd(nil, nil)

	if os.Getenv("TRAVIS") == "true" && runtime.GOOS == "darwin" {
		fmt.Println("Building with native backend")
		execute("go", "build", "-tags=macnative", buildFlags(), DelveMainPackagePath)

		fmt.Println("\nBuilding without native backend")
		execute("go", "build", buildFlags(), DelveMainPackagePath)

		fmt.Println("\nTesting")
		os.Setenv("PROCTEST", "lldb")
		executeq("sudo", "-E", "go", "test", testFlags(), allPackages())
		return
	}

	if TestSet == "" && TestBackend == "" && TestBuildMode == "" {
		if TestRegex != "" {
			fmt.Printf("Can not use --test-run without --test-set\n")
			os.Exit(1)
		}

		testStandard()
		return
	}

	if TestSet == "" {
		TestSet = "all"
	}

	if TestBackend == "" {
		TestBackend = "default"
	}

	if TestBuildMode == "" {
		TestBuildMode = "normal"
	}

	testCmdIntl(TestSet, TestRegex, TestBackend, TestBuildMode)
}

func testStandard() {
	fmt.Println("Testing default backend")
	testCmdIntl("all", "", "default", "normal")
	if inpath("lldb-server") && !goversion.VersionAfterOrEqual(runtime.Version(), 1, 14) {
		fmt.Println("\nTesting LLDB backend")
		testCmdIntl("basic", "", "lldb", "normal")
	}
	if inpath("rr") {
		fmt.Println("\nTesting RR backend")
		testCmdIntl("basic", "", "rr", "normal")
	}
	if TestIncludePIE && (runtime.GOOS == "linux" || (runtime.GOOS == "windows" && goversion.VersionAfterOrEqual(runtime.Version(), 1, 15))) {
		fmt.Println("\nTesting PIE buildmode, default backend")
		testCmdIntl("basic", "", "default", "pie")
		testCmdIntl("core", "", "default", "pie")
	}
	if runtime.GOOS == "linux" && inpath("rr") {
		fmt.Println("\nTesting PIE buildmode, RR backend")
		testCmdIntl("basic", "", "rr", "pie")
	}
}

func testCmdIntl(testSet, testRegex, testBackend, testBuildMode string) {
	testPackages := testSetToPackages(testSet)
	if len(testPackages) == 0 {
		fmt.Printf("Unknown test set %q\n", testSet)
		os.Exit(1)
	}

	if testRegex != "" && len(testPackages) != 1 {
		fmt.Printf("Can not use test-run with test set %q\n", testSet)
		os.Exit(1)
	}

	backendFlag := ""
	if testBackend != "" && testBackend != "default" {
		if testSet != "basic" && len(testPackages) != 1 {
			fmt.Printf("Can not use test-backend with test set %q\n", testSet)
			os.Exit(1)
		}
		backendFlag = "-backend=" + testBackend
	}

	buildModeFlag := ""
	if testBuildMode != "" && testBuildMode != "normal" {
		if testSet != "basic" && len(testPackages) != 1 {
			fmt.Printf("Can not use test-buildmode with test set %q\n", testSet)
			os.Exit(1)
		}
		buildModeFlag = "-test-buildmode=" + testBuildMode
	}

	if len(testPackages) > 3 {
		executeq("go", "test", testFlags(), buildFlags(), testPackages, backendFlag, buildModeFlag)
	} else if testRegex != "" {
		execute("go", "test", testFlags(), buildFlags(), testPackages, "-run="+testRegex, backendFlag, buildModeFlag)
	} else {
		execute("go", "test", testFlags(), buildFlags(), testPackages, backendFlag, buildModeFlag)
	}
}

func testSetToPackages(testSet string) []string {
	switch testSet {
	case "", "all":
		return allPackages()

	case "basic":
		return []string{"github.com/go-delve/delve/pkg/proc", "github.com/go-delve/delve/service/test", "github.com/go-delve/delve/pkg/terminal"}

	case "integration":
		return []string{"github.com/go-delve/delve/service/test"}

	default:
		for _, pkg := range allPackages() {
			if pkg == testSet || strings.HasSuffix(pkg, "/"+testSet) {
				return []string{pkg}
			}
		}
		return nil
	}
}

func defaultBackend() string {
	if runtime.GOOS == "darwin" {
		return "lldb"
	}
	return "native"
}

func inpath(exe string) bool {
	path, _ := exec.LookPath(exe)
	return path != ""
}

func allPackages() []string {
	r := []string{}
	for _, dir := range strings.Split(getoutput("go", "list", "-mod=vendor", "./..."), "\n") {
		dir = strings.TrimSpace(dir)
		if dir == "" || strings.Contains(dir, "/vendor/") || strings.Contains(dir, "/_scripts") {
			continue
		}
		r = append(r, dir)
	}
	sort.Strings(r)
	return r
}

func main() {
	allPackages() // checks that vendor directory is synced as a side effect
	NewMakeCommands().Execute()
}
