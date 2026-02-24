# Stage 1: build
FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go-server/ go-server/
WORKDIR /src/go-server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/server ./cmd/server

# Stage 2: run (static binary, no libc needed)
FROM gcr.io/distroless/static-debian12

COPY --from=builder /bin/server /server

EXPOSE 8080
ENTRYPOINT ["/server"]
