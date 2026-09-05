# Multi-stage build (CLAUDE.md Phase 15): compile the single Go binary,
# then ship only that binary plus its static data on a minimal runtime
# base — no OS packages, no shell, matching the "single binary, no infra
# we don't need" philosophy the rest of this project follows.

FROM golang:1.26-bookworm AS build
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO_ENABLED=0 for a static binary (no cgo dependency in this module —
# gin, the genai SDK, and everything else here are pure Go), so it runs
# on a base with no libc at all.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# distroless/static, not scratch: it still has nothing but the binary's
# runtime needs (CA certificates, tzdata, an /etc/passwd nobody entry) —
# scratch would need those bundled by hand just to make outbound TLS to
# Vertex AI work at all.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server ./server
# web/static is go:embed'd into the binary itself (the web package) —
# only data/fixtures is read from the filesystem at request time and
# needs to actually be present alongside the binary.
COPY data/fixtures ./data/fixtures

# Cloud Run injects $PORT and expects the container to listen on it;
# main.go already reads it (falls back to 8080 for local `docker run`).
EXPOSE 8080
ENTRYPOINT ["./server"]
