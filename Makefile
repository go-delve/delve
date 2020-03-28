.DEFAULT_GOAL=test

check-cert:
	@go run _scripts/make.go check-cert

build:
	@go run _scripts/make.go build

install:
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

.PHONY: vendor test-integration-run test-proc-run test check-cert install build vet
