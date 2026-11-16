GO=GOTOOLCHAIN=local CGO_ENABLED=1 go
.PHONY: build test race vet check demo doctor
build:
	mkdir -p bin
	$(GO) build -o bin/sail ./cmd/sail
test:
	$(GO) test ./...
race:
	$(GO) test -race ./...
vet:
	$(GO) vet ./...
check: test race vet build
demo: build
	./bin/sail paper -db data/demo.db -file testdata/paper-example.json
doctor: build
	./bin/sail doctor
