.DEFAULT_GOAL=test
UNAME = $(shell uname)
PREFIX=github.com/derekparker/delve
GOVERSION = $(shell go version)

# We must compile with -ldflags="-s" to omit
# DWARF info on OSX when compiling with the
# 1.5 toolchain. Otherwise the resulting binary
# will be malformed once we codesign it and
# unable to execute.
# See https://github.com/golang/go/issues/11887#issuecomment-126117692.
ifneq (,$(findstring 1.5, $(GOVERSION)))
FLAGS=-ldflags="-s"
endif

build:
	go build $(FLAGS) github.com/derekparker/delve/cmd/dlv
ifeq "$(UNAME)" "Darwin"
ifeq "$(CERT)" ""
	$(error You must provide a CERT env var)
endif
	codesign -s $(CERT) ./dlv
endif

install:
	go install $(FLAGS) github.com/derekparker/delve/cmd/dlv
ifeq "$(UNAME)" "Darwin"
ifeq "$(CERT)" ""
	$(error You must provide a CERT env var)
endif
	codesign -s $(CERT) $(GOPATH)/bin/dlv
endif

test:
ifeq "$(UNAME)" "Darwin"
ifeq "$(CERT)" ""
	$(error You must provide a CERT env var)
endif
	go test $(PREFIX)/terminal $(PREFIX)/dwarf/frame $(PREFIX)/dwarf/op $(PREFIX)/dwarf/util $(PREFIX)/source $(PREFIX)/dwarf/line
	go test -c $(FLAGS) $(PREFIX)/proc && codesign -s $(CERT) ./proc.test && ./proc.test $(TESTFLAGS) && rm ./proc.test
	go test -c  $(FLAGS) $(PREFIX)/service/test && codesign -s $(CERT) ./test.test && ./test.test $(TESTFLAGS) && rm ./test.test
else
	go test -v ./...
endif

test-proc-run:
ifeq "$(UNAME)" "Darwin"
ifeq "$(CERT)" ""
	$(error You must provide a CERT env var)
endif
	go test -c $(FLAGS) $(PREFIX)/proc && codesign -s $(CERT) ./proc.test && ./proc.test -test.run $(RUN) && rm ./proc.test
else
	go test $(PREFIX) -run $(RUN)
endif

test-integration-run:
ifeq "$(UNAME)" "Darwin"
ifeq "$(CERT)" ""
	$(error You must provide a CERT env var)
endif
	go test -c $(FLAGS) $(PREFIX)/service/test && codesign -s $(CERT) ./test.test && ./test.test -test.run $(RUN) && rm ./test.test
else
	go test $(PREFIX)/service/rest -run $(RUN)
endif
