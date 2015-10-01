profile
=======

Simple profiling support package for Go

installation
------------

    go get github.com/davecheney/profile

usage
-----

Enabling profiling in your application is as simple as one line at the top of your main function

```go
import "github.com/davecheney/profile"

func main() {
    defer profile.Start(profile.CPUProfile).Stop()
    ...
}
```

options
-------

What to profile is controlled by the \*profile.Config value passed to profile.Start. A nil
Config is the same as choosing all the defaults. By default no profiles are enabled.

```go
import "github.com/davecheney/profile"

func main() {
    cfg := profile.Config{
        MemProfile:     true,
        ProfilePath:    ".",  // store profiles in current directory
        NoShutdownHook: true, // do not hook SIGINT
    }

    // p.Stop() must be called before the program exits to
    // ensure profiling information is written to disk.
    p := profile.Start(&cfg)
    ...
}
```

Several convenience package level values are provided for cpu, memory, and block (contention) profiling.

For more complex options, consult the [documentation](http://godoc.org/github.com/davecheney/profile) on the profile.Config type. Enabling more than one profile at once may cause your results to be less reliable as profiling itself is not without overhead.
