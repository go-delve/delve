//go:build ignore

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

type Platform struct {
	GOOS      string
	GOARCH    string
	BuildTags string
}

// All supported platforms (except freebsd)
var platforms = []Platform{
	{GOOS: "linux", GOARCH: "amd64"},
	{GOOS: "linux", GOARCH: "386"},
	{GOOS: "linux", GOARCH: "arm64"},
	{GOOS: "linux", GOARCH: "ppc64le", BuildTags: "exp.linuxppc64le"},
	{GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "darwin", GOARCH: "arm64"},
	{GOOS: "windows", GOARCH: "amd64"},
	{GOOS: "windows", GOARCH: "arm64"},
}

const (
	colorRed    = "\033[0;31m"
	colorGreen  = "\033[0;32m"
	colorYellow = "\033[1;33m"
	colorReset  = "\033[0m"
)

func main() {
	// Find the project root by looking for go.mod
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		log.Fatal("Could not find project root (no go.mod found)")
	}

	// Change to project root
	if err := os.Chdir(projectRoot); err != nil {
		log.Fatalf("Failed to change to project root: %v", err)
	}

	// Check if capslock is installed
	if _, err := exec.LookPath("capslock"); err != nil {
		fmt.Printf("%scapslock is not installed. Installing...%s\n", colorYellow, colorReset)
		cmd := exec.Command("go", "install", "github.com/google/capslock/cmd/capslock@latest")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("%sFailed to install capslock. Please install it manually:%s\n", colorRed, colorReset)
			fmt.Println("go install github.com/google/capslock/cmd/capslock@latest")
			os.Exit(1)
		}
	}

	fmt.Println("Generating capslock output files for all supported platforms...")
	fmt.Println("==================================================")

	successCount := 0
	failureCount := 0
	var failedPlatforms []string

	var wg sync.WaitGroup

	for _, platform := range platforms {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := generateCapslock(platform); err != nil {
				failureCount++
				failedPlatforms = append(failedPlatforms, fmt.Sprintf("%s/%s", platform.GOOS, platform.GOARCH))
				fmt.Printf("Generating for %s/%s... %sFAILED%s\n", platform.GOOS, platform.GOARCH, colorRed, colorReset)
				fmt.Printf("  Error: %v\n", err)
			} else {
				successCount++
				fmt.Printf("Generating for %s/%s... %sSUCCESS%s\n",
					platform.GOOS, platform.GOARCH, colorGreen, colorReset)
			}
		}()
	}
	wg.Wait()

	fmt.Println("==================================================")
	fmt.Println("Generation complete!")
	fmt.Printf("  %sSuccessful: %d%s\n", colorGreen, successCount, colorReset)
	fmt.Printf("  %sFailed: %d%s\n", colorRed, failureCount, colorReset)

	if len(failedPlatforms) > 0 {
		fmt.Println("\nFailed platforms:")
		for _, platform := range failedPlatforms {
			fmt.Printf("  - %s\n", platform)
		}
		fmt.Println("\nCheck the output files for error details.")
	}

	fmt.Println("\nGenerated files:")
	matches, _ := filepath.Glob("_scripts/capslock*.txt")
	for _, match := range matches {
		info, err := os.Stat(match)
		if err == nil {
			fmt.Printf("  %s (%d bytes)\n", match, info.Size())
		}
	}

	if failureCount > 0 {
		os.Exit(1)
	}
}

func generateCapslock(platform Platform) error {
	outputFile := fmt.Sprintf("_scripts/capslock_%s_%s-output.txt", platform.GOOS, platform.GOARCH)

	args := []string{}
	if platform.BuildTags != "" {
		args = append(args, "-buildtags", platform.BuildTags)
	}
	args = append(args, "-packages", "./cmd/dlv")

	cmd := exec.Command("capslock", args...)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("GOOS=%s", platform.GOOS),
		fmt.Sprintf("GOARCH=%s", platform.GOARCH),
	)

	output, err := cmd.CombinedOutput()

	if writeErr := os.WriteFile(outputFile, output, 0644); writeErr != nil {
		return fmt.Errorf("failed to write output file: %w", writeErr)
	}

	if err != nil {
		return fmt.Errorf("capslock failed: %w", err)
	}

	return nil
}

func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return ""
}
