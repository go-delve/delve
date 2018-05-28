package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const DelveMainPackagePath = "github.com/derekparker/delve/cmd/dlv"

var Verbose bool
var TestSet, TestRegex, TestBackend string

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
			checkCertCmd(nil, nil)
			execute("go", "build", buildFlags(), DelveMainPackagePath)
			if runtime.GOOS == "darwin" && os.Getenv("CERT") != "" {
				codesign("./dlv")
			}
		},
	})

	RootCommand.AddCommand(&cobra.Command{
		Use:   "install",
		Short: "Installs delve",
		Run: func(cmd *cobra.Command, args []string) {
			checkCertCmd(nil, nil)
			execute("go", "install", buildFlags(), DelveMainPackagePath)
			if runtime.GOOS == "darwin" {
				codesign(installedExecutablePath())
			}
		},
	})

	test := &cobra.Command{
		Use:   "test",
		Short: "Tests delve",
		Long: `Tests delve.

Use the flags -s, -r and -b to specify which tests to run. Specifying nothing is equivalent to:

	go run scripts/make.go test -s all -b default
	go run scripts/make.go test -s basic -b lldb
	go run scripts/make.go test -s basic -b rr

with lldb and rr tests only run if the relevant programs are installed.`,
		Run: testCmd,
	}
	test.PersistentFlags().BoolVarP(&Verbose, "verbose", "v", false, "Verbose tests")
	test.PersistentFlags().StringVarP(&TestSet, "test-set", "s", "", `Select the set of tests to run, one of either:
	all		tests all packages
	basic		tests proc, integration and terminal
	integration 	tests github.com/derekparker/delve/service/test
	package-name	test the specified package only
`)
	test.PersistentFlags().StringVarP(&TestRegex, "test-run", "r", "", `Only runs the tests matching the specified regex. This option can only be specified if testset is a single package`)
	test.PersistentFlags().StringVarP(&TestBackend, "test-backend", "b", "", `Runs tests for the specified backend only, one of either:
	default		the default backend
	lldb		lldb backend
	rr		rr backend

This option can only be specified if testset is basic or a single package.`)

	RootCommand.AddCommand(test)

	RootCommand.AddCommand(&cobra.Command{
		Use:   "vendor",
		Short: "vendors dependencies",
		Run: func(cmd *cobra.Command, args []string) {
			execute("glide", "up", "-v")
			execute("glide-vc", "--use-lock-file", "--no-tests", "--only-code")
		},
	})

	return RootCommand
}

func checkCertCmd(cmd *cobra.Command, args []string) {
	// If we're on OSX make sure the proper CERT env var is set.
	if os.Getenv("TRAVIS") == "true" || runtime.GOOS != "darwin" || os.Getenv("CERT") != "" {
		return
	}

	x := exec.Command("scripts/gencert.sh")
	err := x.Run()
	if x.ProcessState != nil && !x.ProcessState.Success() {
		fmt.Printf("An error occurred when generating and installing a new certificate\n")
		os.Exit(1)
	}
	if err != nil {
		log.Fatal(err)
	}
	os.Setenv("CERT", "dlv-cert")
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
	out, err := x.CombinedOutput()
	if err != nil {
		log.Fatal(err)
	}
	if !x.ProcessState.Success() {
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
	return filepath.Join(gopath[0], "dlv")
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
	if runtime.GOOS == "darwin" {
		testFlags = append(testFlags, "-exec="+wd+"/scripts/testsign")
	}
	return testFlags
}

func testCmd(cmd *cobra.Command, args []string) {
	checkCertCmd(nil, nil)

	if os.Getenv("TRAVIS") == "true" && runtime.GOOS == "darwin" {
		os.Setenv("PROCTEST", "lldb")
		executeq("sudo", "-E", "go", "test", testFlags(), allPackages())
		return
	}

	if TestSet == "" && TestBackend == "" {
		if TestRegex != "" {
			fmt.Printf("Can not use --test-run without --test-set\n")
			os.Exit(1)
		}

		fmt.Println("Testing default backend")
		testCmdIntl("all", "", "default")
		if inpath("lldb-server") {
			fmt.Println("\nTesting LLDB backend")
			testCmdIntl("basic", "", "lldb")
		}
		if inpath("rr") {
			fmt.Println("\nTesting RR backend")
			testCmdIntl("basic", "", "rr")
		}
		return
	}

	if TestSet == "" {
		TestSet = "all"
	}

	if TestBackend == "" {
		TestBackend = "default"
	}

	testCmdIntl(TestSet, TestRegex, TestBackend)
}

func testCmdIntl(testSet, testRegex, testBackend string) {
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

	if len(testPackages) > 3 {
		execute("go", "test", testFlags(), buildFlags(), testPackages, backendFlag)
	} else if testRegex != "" {
		execute("go", "test", testFlags(), buildFlags(), testPackages, "-run="+testRegex, backendFlag)
	} else {
		execute("go", "test", testFlags(), buildFlags(), testPackages, backendFlag)
	}
}

func testSetToPackages(testSet string) []string {
	switch testSet {
	case "", "all":
		return allPackages()

	case "basic":
		return []string{"github.com/derekparker/delve/pkg/proc", "github.com/derekparker/delve/service/test", "github.com/derekparker/delve/pkg/terminal"}

	case "integration":
		return []string{"github.com/derekparker/delve/service/test"}

	default:
		for _, pkg := range allPackages() {
			if pkg == testSet || strings.HasSuffix(pkg, "/"+testSet) {
				return []string{pkg}
			}
		}
		return nil
	}
}

func inpath(exe string) bool {
	path, _ := exec.LookPath(exe)
	return path != ""
}

func allPackages() []string {
	r := []string{}
	for _, dir := range strings.Split(getoutput("go", "list", "./..."), "\n") {
		dir = strings.TrimSpace(dir)
		if dir == "" || strings.Contains(dir, "/vendor/") || strings.Contains(dir, "/scripts") {
			continue
		}
		r = append(r, dir)
	}
	sort.Strings(r)
	return r
}

func main() {
	NewMakeCommands().Execute()
}
