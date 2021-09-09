FROM golang:1.16-alpine
RUN apk --no-cache update && apk --no-cache add clang llvm make gcc libc6-compat coreutils linux-headers musl-dev elfutils-dev libelf-static zlib-static make libbpf-dev libbpf git

WORKDIR /delve

CMD [ "/usr/bin/make", "build-bpf" ]