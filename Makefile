GOLANGCI_LINT_VERSION := v2.11.4
GOLANGCI_LINT := ./bin/golangci-lint

.PHONY: check lint lint-install test test-coverage

check:
	gofmt -w ./cmd ./internal
	go vet ./...
	$(GOLANGCI_LINT) run ./...
	go test ./...

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run ./...

lint-install: $(GOLANGCI_LINT)

$(GOLANGCI_LINT):
	GOBIN=$(CURDIR)/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

test:
	go test ./...

test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out | awk '/^total:/ { split($$3, pct, "%"); if (pct[1] + 0 < 80) { printf("coverage %.1f%% is below 80%%\n", pct[1]); exit 1 } printf("coverage %.1f%% meets 80%% threshold\n", pct[1]); }'
