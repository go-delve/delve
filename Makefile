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
ifneq (,$(findstring 1.5, $(GOVERSION)))
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

build: check-cert
	gb build $(FLAGS) 
ifeq "$(UNAME)" "Darwin"
	codesign -s $(CERT) ./bin/dlv
endif

install: build
	cp ./bin/dlv $(GOPATH)/bin/dlv
ifeq "$(UNAME)" "Darwin"
	codesign -s $(CERT) $(GOPATH)/bin/dlv
endif

test: check-cert
ifeq "$(UNAME)" "Darwin"
ifeq "$(TRAVIS)" "true"
	sudo -E gb test -v
else
	gb test $(PREFIX)/terminal $(PREFIX)/dwarf/frame $(PREFIX)/dwarf/op $(PREFIX)/dwarf/util $(PREFIX)/source $(PREFIX)/dwarf/line
	gb test -c $(FLAGS) $(PREFIX)/proc && codesign -s $(CERT) ./proc.test && ./proc.test $(TESTFLAGS) -test.v && rm ./proc.test
	gb test -c  $(FLAGS) $(PREFIX)/service/test && codesign -s $(CERT) ./test.test && ./test.test $(TESTFLAGS) -test.v && rm ./test.test
endif
else
	go test -v ./...
endif

test-proc-run: check-cert
ifeq "$(UNAME)" "Darwin"
	go test -c $(FLAGS) $(PREFIX)/proc && codesign -s $(CERT) ./proc.test && ./proc.test -test.run $(RUN) && rm ./proc.test
else
	go test $(PREFIX) -run $(RUN)
endif

test-integration-run: check-cert
ifeq "$(UNAME)" "Darwin"
	go test -c $(FLAGS) $(PREFIX)/service/test && codesign -s $(CERT) ./test.test && ./test.test -test.run $(RUN) && rm ./test.test
else
	go test $(PREFIX)/service/rest -run $(RUN)
endif
