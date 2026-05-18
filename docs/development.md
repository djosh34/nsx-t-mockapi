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
