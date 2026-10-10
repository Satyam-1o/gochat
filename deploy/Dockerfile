# Build stage.
FROM golang:1.27 AS build

WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

# Build the static rendezvous/relay node binary.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" \
        -o /out/gochat-rendezvous ./cmd/gochat-rendezvous

# Runtime stage.
FROM debian:stable-slim

RUN useradd --system --uid 10001 --home /data --create-home --shell /usr/sbin/nologin gochat

COPY --from=build /out/gochat-rendezvous /usr/local/bin/gochat-rendezvous

RUN mkdir -p /data && chown gochat:gochat /data
VOLUME ["/data"]

USER gochat
WORKDIR /data

# libp2p listens on both TCP and QUIC/UDP.
EXPOSE 4001/tcp 4001/udp

# The identity key (/data/rendezvous.key) persists across restarts, keeping
# the peer ID — and therefore your clients' saved bootstrap address — stable.
#
# Set -public to your public IP/DNS, or pass explicit -announce multiaddrs:
#   docker run -p 4001:4001/tcp -p 4001:4001/udp \
#     -v gochat-data:/data gochat-rendezvous -public 203.0.113.5
ENTRYPOINT ["/usr/local/bin/gochat-rendezvous"]
CMD ["-port", "4001", "-data", "/data"]
