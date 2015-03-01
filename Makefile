UNAME = $(shell uname)
PREFIX=github.com/derekparker/delve

build:
	go build github.com/derekparker/delve/cmd/dlv
ifeq "$(UNAME)" "Darwin"
	codesign -s dlv-cert ./dlv
endif

install:
	go install github.com/derekparker/delve/cmd/dlv
ifeq "$(UNAME)" "Darwin"
	codesign -s dlv-cert $(which dlv)
endif

test:
ifeq "$(UNAME)" "Darwin"
	go test $(PREFIX)/command $(PREFIX)/dwarf/frame $(PREFIX)/dwarf/op $(PREFIX)/dwarf/util
	cd proctl && go test -c $(PREFIX)/proctl && codesign -s $(CERT) ./proctl.test && ./proctl.test
else
	go test ./...
endif
