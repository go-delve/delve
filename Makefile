.DEFAULT_GOAL=test

check-cert:
	@go run scripts/make.go check-cert

build:
	@go run scripts/make.go build

install:
	@go run scripts/make.go install

uninstall:
	@go run scripts/make.go uninstall

test: vet
	@go run scripts/make.go test

vet:
	@go vet $$(go list ./... | grep -v scripts | grep -v native)

test-proc-run:
	@go run scripts/make.go test -s proc -r $(RUN)

test-integration-run:
	@go run scripts/make.go test -s service/test -r $(RUN)

vendor:
	@go run scripts/make.go vendor

.PHONY: vendor test-integration-run test-proc-run test check-cert install build vet
