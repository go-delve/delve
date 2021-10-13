TARGET_BPF := test/test.bpf.o
VMLINUX_H = test/vmlinux.h

GO_SRC := $(shell find . -type f -name '*.go')
BPF_SRC := $(shell find . -type f -name '*.bpf.c')
PWD := $(shell pwd)

LIBBPF_HEADERS := /usr/include/bpf
LIBBPF := "-lbpf"

.PHONY: all
all: test

$(VMLINUX_H):
	bpftool btf dump file /sys/kernel/btf/vmlinux format c > test/vmlinux.h

go_env := CC=gcc CGO_CFLAGS="-I $(LIBBPF_HEADERS)" CGO_LDFLAGS="$(LIBBPF)"
.PHONY: test
test: $(TARGET_BPF) $(GO_SRC)
	$(go_env) go test -ldflags '-extldflags "-static"' .

$(TARGET_BPF): $(BPF_SRC) $(VMLINUX_H)
	clang \
		-g -O2 -c -target bpf \
		-o $@ $<

.PHONY: clean
clean: 
	rm $(TARGET_BPF) $(VMLINUX_H)
