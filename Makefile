.DEFAULT_GOAL=test
UNAME=$(shell uname)
PREFIX=github.com/derekparker/delve
GOPATH=$(shell go env GOPATH)
GOVERSION=$(shell go version)
BUILD_SHA=$(shell git rev-parse HEAD)
LLDB_SERVER=$(shell which lldb-server)

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
	TEST_FLAGS=-count 1 -exec=$(shell pwd)/scripts/testsign
	export PROCTEST=lldb
	DARWIN="true"
else
	TEST_FLAGS=-count 1
endif

# If we're on OSX make sure the proper CERT env var is set.
check-cert:
ifneq "$(TRAVIS)" "true"
ifdef DARWIN
ifeq "$(CERT)" ""
	scripts/gencert.sh || (echo "An error occurred when generating and installing a new certificate"; exit 1)
        CERT = dlv-cert
endif
endif
endif

build: check-cert
	go build $(BUILD_FLAGS) github.com/derekparker/delve/cmd/dlv
ifdef DARWIN
ifdef CERT
	codesign -s "$(CERT)"  ./dlv
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
	sudo -E go test -p 1 -count 1 -v $(ALL_PACKAGES)
else
	go test -p 1 $(TEST_FLAGS) $(BUILD_FLAGS) $(ALL_PACKAGES)
endif
else
	go test -p 1 $(TEST_FLAGS) $(BUILD_FLAGS) $(ALL_PACKAGES)
endif
ifneq "$(shell which lldb-server 2>/dev/null)" ""
	@echo
	@echo 'Testing LLDB backend (proc)'
	go test $(TEST_FLAGS) $(BUILD_FLAGS) $(PREFIX)/pkg/proc -backend=lldb
	@echo
	@echo 'Testing LLDB backend (integration)'
	go test $(TEST_FLAGS) $(BUILD_FLAGS) $(PREFIX)/service/test -backend=lldb
	@echo
	@echo 'Testing LLDB backend (terminal)'
	go test $(TEST_FLAGS) $(BUILD_FLAGS) $(PREFIX)/pkg/terminal -backend=lldb
endif
ifneq "$(shell which rr 2>/dev/null)" ""
	@echo
	@echo 'Testing Mozilla RR backend (proc)'
	go test $(TEST_FLAGS) $(BUILD_FLAGS) $(PREFIX)/pkg/proc -backend=rr
	@echo
	@echo 'Testing Mozilla RR backend (integration)'
	go test $(TEST_FLAGS) $(BUILD_FLAGS) $(PREFIX)/service/test -backend=rr
	@echo
	@echo 'Testing Mozilla RR backend (terminal)'
	go test $(TEST_FLAGS) $(BUILD_FLAGS) $(PREFIX)/pkg/terminal -backend=rr
endif

test-proc-run:
	go test $(TEST_FLAGS) $(BUILD_FLAGS) -test.v -test.run="$(RUN)" -backend=$(BACKEND) $(PREFIX)/pkg/proc

test-integration-run:
	go test $(TEST_FLAGS) $(BUILD_FLAGS) -test.run="$(RUN)" -backend=$(BACKEND) $(PREFIX)/service/test

vendor: glide.yaml
	@glide up -v
	@glide-vc --use-lock-file --no-tests --only-code

.PHONY: vendor test-integration-run test-proc-run test check-cert install build
