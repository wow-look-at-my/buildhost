FROM golang:1.24 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /buildhost ./cmd/buildhost

FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends binutils ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /buildhost /usr/local/bin/buildhost

EXPOSE 8080
ENTRYPOINT ["buildhost"]
CMD ["serve"]
