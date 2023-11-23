# Want to contribute?

We welcome tickets, pull requests, feature suggestions.

When developing, please try to comply to the general code style that we try to
maintain across the project. When introducing new features or fixing
significant bugs, please provide tests and also include some concise
information.

## Test resources

A lot of tests in the library requires test resources. Due to the total size of
these files, they are not included with the source code. The `testdata` folder
contains a helper script that can be used to build them. For building the
resources locally `docker` is used. They are also stored in a separate repo that
can be accessed by running: `git submodule update --init --recursive`

Build missing resources:
```
./testdata/helper.sh build
```

## Download the latest Go compiler release information

```
go generate
```
