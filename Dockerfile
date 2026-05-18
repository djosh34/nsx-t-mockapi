FROM golang:1.26.3 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS
ARG TARGETARCH

RUN target_os="${TARGETOS:-linux}"; \
    target_arch="${TARGETARCH:-$(go env GOARCH)}"; \
    CGO_ENABLED=0 GOOS="${target_os}" GOARCH="${target_arch}" \
    go build -trimpath -ldflags="-s -w" -o /out/nsx-t-mockapi ./cmd/nsx-t-mockapi

FROM scratch

COPY --from=builder /out/nsx-t-mockapi /nsx-t-mockapi

ENTRYPOINT ["/nsx-t-mockapi"]
