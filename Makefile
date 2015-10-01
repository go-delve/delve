.DEFAULT_GOAL=test
UNAME=$(shell uname)
PREFIX=github.com/derekparker/delve
GOVERSION=$(shell go version)

# Workaround for GO15VENDOREXPERIMENT bug (https://github.com/golang/go/issues/11659)
ALL_PACKAGES=$(shell go list ./... | grep -v /vendor/)

# We must compile with -ldflags="-s" to omit
# DWARF info on OSX when compiling with the
# 1.5 toolchain. Otherwise the resulting binary
# will be malformed once we codesign it and
# unable to execute.
# See https://github.com/golang/go/issues/11887#issuecomment-126117692.
ifeq "$(UNAME)" "Darwin"
	FLAGS=-ldflags="-s"
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
	go build $(FLAGS) github.com/derekparker/delve/cmd/dlv
ifdef DARWIN
	codesign -s $(CERT) ./dlv
endif

install: check-cert
	go install $(FLAGS) github.com/derekparker/delve/cmd/dlv
ifdef DARWIN
	codesign -s $(CERT) $(GOPATH)/bin/dlv
endif

test: check-cert
ifdef DARWIN
ifeq "$(TRAVIS)" "true"
	sudo -E go test -v $(ALL_PACKAGES)
else
	go test $(PREFIX)/terminal $(PREFIX)/dwarf/frame $(PREFIX)/dwarf/op $(PREFIX)/dwarf/util $(PREFIX)/source $(PREFIX)/dwarf/line
	go test -c $(FLAGS) $(PREFIX)/proc && codesign -s $(CERT) ./proc.test && ./proc.test $(TESTFLAGS) -test.v && rm ./proc.test
	go test -c  $(FLAGS) $(PREFIX)/service/test && codesign -s $(CERT) ./test.test && ./test.test $(TESTFLAGS) -test.v && rm ./test.test
endif
else
	go test -v $(ALL_PACKAGES)
endif

test-proc-run:
ifdef DARWIN
	go test -c $(FLAGS) $(PREFIX)/proc && codesign -s $(CERT) ./proc.test && ./proc.test -test.run $(RUN) && rm ./proc.test
else
	go test $(PREFIX) -run $(RUN)
endif

test-integration-run:
ifdef DARWIN
	go test -c $(FLAGS) $(PREFIX)/service/test && codesign -s $(CERT) ./test.test && ./test.test -test.run $(RUN) && rm ./test.test
else
	go test $(PREFIX)/service/rest -run $(RUN)
endif
