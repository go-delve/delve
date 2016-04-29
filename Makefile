.DEFAULT_GOAL=test
UNAME=$(shell uname)
PREFIX=github.com/derekparker/delve
GOVERSION=$(shell go version)
BUILD_SHA=$(shell git rev-parse HEAD)

ifeq "$(UNAME)" "Darwin"
    BUILD_FLAGS=-ldflags="-s -X main.Build=$(BUILD_SHA)"
else
    BUILD_FLAGS=-ldflags="-X main.Build=$(BUILD_SHA)"
endif

# Workaround for GO15VENDOREXPERIMENT bug (https://github.com/golang/go/issues/11659)
ALL_PACKAGES=$(shell go list ./... | grep -v /vendor/ | grep -v /scripts)

# We must compile with -ldflags="-s" to omit
# DWARF info on OSX when compiling with the
# 1.5 toolchain. Otherwise the resulting binary
# will be malformed once we codesign it and
# unable to execute.
# See https://github.com/golang/go/issues/11887#issuecomment-126117692.
ifeq "$(UNAME)" "Darwin"
	TEST_FLAGS=-exec=$(shell pwd)/scripts/testsign
	DARWIN="true"
endif

# If we're on OSX make sure the proper CERT env var is set.
check-cert:
ifneq "$(TRAVIS)" "true"
ifdef DARWIN
ifeq "$(CERT)" ""
	$(error You must provide a CERT environment variable in order to codesign the binary.)
endif
endif
endif

build: check-cert
	go build $(BUILD_FLAGS) github.com/derekparker/delve/cmd/dlv
ifdef DARWIN
ifneq "$(GOBIN)" ""
	codesign -s "$(CERT)"  $(GOBIN)/dlv
else
	codesign -s "$(CERT)"  $(GOPATH)/bin/dlv
endif
endif

install: check-cert
	go install $(BUILD_FLAGS) github.com/derekparker/delve/cmd/dlv
ifdef DARWIN
ifneq "$(GOBIN)" ""
	codesign -s "$(CERT)"  $(GOBIN)/dlv
else
	codesign -s "$(CERT)"  $(GOPATH)/bin/dlv
endif
endif

test: check-cert
ifeq "$(TRAVIS)" "true"
ifdef DARWIN
	sudo -E go test -v $(ALL_PACKAGES)
else
	go test $(TEST_FLAGS) $(BUILD_FLAGS) $(ALL_PACKAGES)
endif
else
	go test $(TEST_FLAGS) $(BUILD_FLAGS) $(ALL_PACKAGES)
endif

test-proc-run:
	go test $(TEST_FLAGS) $(BUILD_FLAGS) -test.run="$(RUN)" $(PREFIX)/proc

test-integration-run:
	go test $(TEST_FLAGS) $(BUILD_FLAGS) -test.run="$(RUN)" $(PREFIX)/service/test
