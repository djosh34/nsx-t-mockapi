# Development

This repository uses a project-local `golangci-lint` binary so local
development and CI can use the same lint version without committing tools.

Run the quality gates from the repository root:

```bash
make lint-install
make lint
make check
make test
make test-coverage
```

`make lint-install` installs `golangci-lint` into `./bin/golangci-lint`.
`make check` formats Go files, runs `go vet`, runs lint, and runs tests.

## Test container

Build the test-only image when you want a self-contained quality gate runner:

```bash
docker build -f Dockerfile.test -t nsx-t-mockapi:test .
```

The image build installs the pinned `golangci-lint` version, downloads module
dependencies, builds the Go commands, and compiles test binaries without
running tests. Start the container to run the quality gates and use the
container exit code as the test result:

```bash
docker run --rm nsx-t-mockapi:test
```

The container disables Go module network access, logs each gate to stderr as
JSON lines, then runs `gofmt` checks, `go vet`, `golangci-lint`, and
`go test ./...`.
