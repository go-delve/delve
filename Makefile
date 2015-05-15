.DEFAULT_GOAL=test
UNAME = $(shell uname)
PREFIX=github.com/derekparker/delve

build:
	go build github.com/derekparker/delve/cmd/dlv
ifeq "$(UNAME)" "Darwin"
	codesign -s $(CERT) ./dlv
endif

install:
	go install github.com/derekparker/delve/cmd/dlv
ifeq "$(UNAME)" "Darwin"
	codesign -s $(CERT) $(GOPATH)/bin/dlv
endif

test:
ifeq "$(UNAME)" "Darwin"
	go test $(PREFIX)/terminal $(PREFIX)/dwarf/frame $(PREFIX)/dwarf/op $(PREFIX)/dwarf/util $(PREFIX)/source $(PREFIX)/dwarf/line
	cd proctl && go test -c $(PREFIX)/proctl && codesign -s $(CERT) ./proctl.test && ./proctl.test $(TESTFLAGS) && rm ./proctl.test
	cd service/rest && go test -c $(PREFIX)/service/rest && codesign -s $(CERT) ./rest.test && ./rest.test $(TESTFLAGS) && rm ./rest.test
else
	go test -v ./...
endif

test-proctl-run:
	cd proctl && go test -c $(PREFIX)/proctl && codesign -s $(CERT) ./proctl.test && ./proctl.test -test.run $(RUN) && rm ./proctl.test

test-integration-run:
	cd service/rest && go test -c $(PREFIX)/service/rest && codesign -s $(CERT) ./rest.test && ./rest.test -test.run $(RUN) && rm ./rest.test
