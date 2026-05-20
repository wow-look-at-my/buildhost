FROM golang:1.25 AS builder

ADD https://github.com/wow-look-at-my/go-toolchain/releases/latest/download/go-toolchain_linux_amd64 /usr/local/bin/go-toolchain
RUN chmod +x /usr/local/bin/go-toolchain

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go-toolchain --generate c29114ff2f33

FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends binutils ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /src/build/buildhost /usr/local/bin/buildhost

EXPOSE 8080
ENTRYPOINT ["buildhost"]
CMD ["serve"]
