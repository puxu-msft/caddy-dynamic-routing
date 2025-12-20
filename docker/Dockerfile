# Build stage
FROM golang:1.25-alpine AS builder
ENV GOTOOLCHAIN=auto

RUN apk add --no-cache git

# Install xcaddy
RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest

# Copy source code
WORKDIR /src
COPY . .

# Build Caddy with the plugin
RUN xcaddy build --with github.com/puxu-msft/caddy-dynamic-routing=./

# Runtime stage
FROM alpine:latest

RUN apk add --no-cache ca-certificates curl

COPY --from=builder /src/caddy /usr/bin/caddy

EXPOSE 8080 2019

CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile"]
