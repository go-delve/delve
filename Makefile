.DEFAULT_GOAL=test

BPF_OBJ := pkg/proc/internal/ebpf/trace_probe/trace.o
BPF_SRC := $(shell find . -type f -name '*.bpf.*')
GO_SRC := $(shell find . -type f -not -path './_fixtures/*' -not -path './vendor/*' -not -path './_scripts/*' -not -path './localtests/*' -name '*.go')

check-cert:
	@go run _scripts/make.go check-cert

build: $(GO_SRC)
	@go run _scripts/make.go build

$(BPF_OBJ): $(BPF_SRC)
	clang \
		-I /usr/include \
		-I /usr/src/kernels/$(uname -r)/tools/lib \
		-I /usr/src/kernels/$(uname -r)/tools/bpf/resolve_btfids/libbpf \
		-g -O2 \
		-c \
		-target bpf \
		-o $(BPF_OBJ) \
		pkg/proc/internal/ebpf/trace_probe/trace.bpf.c

build-bpf: $(BPF_OBJ) $(GO_SRC)
	@env CGO_LDFLAGS="/usr/lib64/libbpf.a" go run _scripts/make.go build --tags=ebpf

install: $(GO_SRC)
	@go run _scripts/make.go install

uninstall:
	@go run _scripts/make.go uninstall

test: vet
	@go run _scripts/make.go test

vet:
	@go vet $$(go list ./... | grep -v native)

test-proc-run:
	@go run _scripts/make.go test -s proc -r $(RUN)

test-integration-run:
	@go run _scripts/make.go test -s service/test -r $(RUN)

vendor:
	@go run _scripts/make.go vendor

.PHONY: vendor test-integration-run test-proc-run test check-cert install build vet build-bpf uninstall
