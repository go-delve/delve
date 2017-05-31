FROM golang:1.5

RUN go get github.com/tools/godep

ENV GO15VENDOREXPERIMENT=1

WORKDIR /go/src/github.com/derekparker/delve

CMD ["make", "build"]
