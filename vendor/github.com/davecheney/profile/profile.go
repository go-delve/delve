// Package profile provides a simple way to manage runtime/pprof
// profiling of your Go application.
package profile

import (
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
)

// Config controls the operation of the profile package.
type Config struct {
	// Quiet suppresses informational messages during profiling.
	Quiet bool

	// CPUProfile controls if cpu profiling will be enabled.
	// It defaults to false.
	CPUProfile bool

	// MemProfile controls if memory profiling will be enabled.
	// It defaults to false.
	MemProfile bool

	// BlockProfile controls if block (contention) profiling will
	// be enabled.
	// It defaults to false.
	BlockProfile bool

	// ProfilePath controls the base path where various profiling
	// files are written. If blank, the base path will be generated
	// by ioutil.TempDir.
	ProfilePath string

	// NoShutdownHook controls whether the profiling package should
	// hook SIGINT to write profiles cleanly.
	// Programs with more sophisticated signal handling should set
	// this to true and ensure the Stop() function returned from Start()
	// is called during shutdown.
	NoShutdownHook bool
}

var zeroConfig Config

const memProfileRate = 4096

func defaultConfig() *Config { return &zeroConfig }

var (
	CPUProfile = &Config{
		CPUProfile: true,
	}

	MemProfile = &Config{
		MemProfile: true,
	}

	BlockProfile = &Config{
		BlockProfile: true,
	}
)

type profile struct {
	path string
	*Config
	closers []func()
}

func (p *profile) Stop() {
	for _, c := range p.closers {
		c()
	}
}

// Start starts a new profiling session configured using *Config.
// The caller should call the Stop method on the value returned
// to cleanly stop profiling.
// Passing a nil *Config is the same as passing a *Config with
// defaults chosen.
func Start(cfg *Config) interface {
	Stop()
} {
	if cfg == nil {
		cfg = defaultConfig()
	}
	path := cfg.ProfilePath
	var err error
	if path == "" {
		path, err = ioutil.TempDir("", "profile")
	} else {
		err = os.MkdirAll(path, 0777)
	}
	if err != nil {
		log.Fatalf("profile: could not create initial output directory: %v", err)
	}
	prof := &profile{
		path:   path,
		Config: cfg,
	}

	if prof.CPUProfile {
		fn := filepath.Join(prof.path, "cpu.pprof")
		f, err := os.Create(fn)
		if err != nil {
			log.Fatalf("profile: could not create cpu profile %q: %v", fn, err)
		}
		if !prof.Quiet {
			log.Printf("profile: cpu profiling enabled, %s", fn)
		}
		pprof.StartCPUProfile(f)
		prof.closers = append(prof.closers, func() {
			pprof.StopCPUProfile()
			f.Close()
		})
	}

	if prof.MemProfile {
		fn := filepath.Join(prof.path, "mem.pprof")
		f, err := os.Create(fn)
		if err != nil {
			log.Fatalf("profile: could not create memory profile %q: %v", fn, err)
		}
		old := runtime.MemProfileRate
		runtime.MemProfileRate = memProfileRate
		if !prof.Quiet {
			log.Printf("profile: memory profiling enabled, %s", fn)
		}
		prof.closers = append(prof.closers, func() {
			pprof.Lookup("heap").WriteTo(f, 0)
			f.Close()
			runtime.MemProfileRate = old
		})
	}

	if prof.BlockProfile {
		fn := filepath.Join(prof.path, "block.pprof")
		f, err := os.Create(fn)
		if err != nil {
			log.Fatalf("profile: could not create block profile %q: %v", fn, err)
		}
		runtime.SetBlockProfileRate(1)
		if !prof.Quiet {
			log.Printf("profile: block profiling enabled, %s", fn)
		}
		prof.closers = append(prof.closers, func() {
			pprof.Lookup("block").WriteTo(f, 0)
			f.Close()
			runtime.SetBlockProfileRate(0)
		})
	}

	if !prof.NoShutdownHook {
		go func() {
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt)
			<-c

			log.Println("profile: caught interrupt, stopping profiles")
			prof.Stop()

			os.Exit(0)
		}()
	}

	return prof
}
