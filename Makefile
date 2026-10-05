.PHONY: run build format vet test check migrate e2e-admin

run:
	go run ./cmd/herald

build:
	go build -o bin/herald ./cmd/herald
	go build -o bin/heraldctl ./cmd/heraldctl

format:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

vet:
	go vet ./...

test:
	go test -count=1 ./...

check: vet test build

# Writes an inspectable JSON event log under artifacts/e2e/.
e2e-admin:
	./scripts/test-admin-e2e.sh

# Applies every unapplied migration against DATABASE_URL.
migrate:
	go run ./cmd/heraldctl migrate
