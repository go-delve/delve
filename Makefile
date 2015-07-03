.DEFAULT_GOAL=test
UNAME = $(shell uname)
PREFIX=github.com/derekparker/delve

build:
	go build github.com/derekparker/delve/cmd/dlv
ifeq "$(UNAME)" "Darwin"
ifeq "$(CERT)" ""
	$(error You must provide a CERT env var)
endif
	codesign -s $(CERT) ./dlv
endif

install:
	go install github.com/derekparker/delve/cmd/dlv
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
	go test -c $(PREFIX)/proc && codesign -s $(CERT) ./proc.test && ./proc.test $(TESTFLAGS) && rm ./proc.test
	go test -c $(PREFIX)/service/test && codesign -s $(CERT) ./test.test && ./test.test $(TESTFLAGS) && rm ./test.test
else
	go test -v ./...
endif

test-proc-run:
ifeq "$(UNAME)" "Darwin"
ifeq "$(CERT)" ""
	$(error You must provide a CERT env var)
endif
	go test -c $(PREFIX)/proc && codesign -s $(CERT) ./proc.test && ./proc.test -test.run $(RUN) && rm ./proc.test
else
	go test $(PREFIX) -run $(RUN)
endif

test-integration-run:
ifeq "$(UNAME)" "Darwin"
ifeq "$(CERT)" ""
	$(error You must provide a CERT env var)
endif
	go test -c $(PREFIX)/service/test && codesign -s $(CERT) ./test.test && ./test.test -test.run $(RUN) && rm ./test.test
else
	go test $(PREFIX)/service/rest -run $(RUN)
endif
