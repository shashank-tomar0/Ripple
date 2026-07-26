# Ripple Go daemon — multi-stage Docker build
# ============================================
# Stage 1: Build the rippled binary
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /rippled ./cmd/rippled

# Stage 2: Minimal runtime image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /rippled /usr/local/bin/rippled

EXPOSE 9000 9876

VOLUME ["/data"]

ENTRYPOINT ["rippled"]
CMD ["-data", "/data", "-db", "/data/ripple.db"]
