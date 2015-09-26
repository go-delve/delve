.DEFAULT_GOAL=test
UNAME=$(shell uname)
PREFIX=github.com/derekparker/delve
GOVERSION=$(shell go version)

# We must compile with -ldflags="-s" to omit
# DWARF info on OSX when compiling with the
# 1.5 toolchain. Otherwise the resulting binary
# will be malformed once we codesign it and
# unable to execute.
# See https://github.com/golang/go/issues/11887#issuecomment-126117692.
ifeq "$(UNAME)" "Darwin"
	FLAGS=-ldflags="-s"
endif

# If we're on OSX make sure the proper CERT env var is set.
check-cert:
ifneq "$(TRAVIS)" "true"
ifeq "$(UNAME)" "Darwin"
ifeq "$(CERT)" ""
	$(error You must provide a CERT environment variable in order to codesign the binary.)
endif
endif
endif

deps: check-cert
ifeq "$(SKIP_DEPS)" ""
	go get -u github.com/peterh/liner
	go get -u github.com/spf13/cobra
	go get -u golang.org/x/sys/unix
	go get -u github.com/davecheney/profile
	go get -u gopkg.in/yaml.v2
endif

build: deps
	go build $(FLAGS) github.com/derekparker/delve/cmd/dlv
ifeq "$(UNAME)" "Darwin"
	codesign -s $(CERT) ./dlv
endif

install: deps
	go install $(FLAGS) github.com/derekparker/delve/cmd/dlv
ifeq "$(UNAME)" "Darwin"
	codesign -s $(CERT) $(GOPATH)/bin/dlv
endif

test: deps
ifeq "$(UNAME)" "Darwin"
ifeq "$(TRAVIS)" "true"
	sudo -E go test -v ./...
else
	go test $(PREFIX)/terminal $(PREFIX)/dwarf/frame $(PREFIX)/dwarf/op $(PREFIX)/dwarf/util $(PREFIX)/source $(PREFIX)/dwarf/line
	go test -c $(FLAGS) $(PREFIX)/proc && codesign -s $(CERT) ./proc.test && ./proc.test $(TESTFLAGS) -test.v && rm ./proc.test
	go test -c  $(FLAGS) $(PREFIX)/service/test && codesign -s $(CERT) ./test.test && ./test.test $(TESTFLAGS) -test.v && rm ./test.test
endif
else
	go test -v ./...
endif

test-proc-run:
ifeq "$(UNAME)" "Darwin"
	go test -c $(FLAGS) $(PREFIX)/proc && codesign -s $(CERT) ./proc.test && ./proc.test -test.run $(RUN) && rm ./proc.test
else
	go test $(PREFIX) -run $(RUN)
endif

test-integration-run:
ifeq "$(UNAME)" "Darwin"
	go test -c $(FLAGS) $(PREFIX)/service/test && codesign -s $(CERT) ./test.test && ./test.test -test.run $(RUN) && rm ./test.test
else
	go test $(PREFIX)/service/rest -run $(RUN)
endif
