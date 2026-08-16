# Build stage
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /pocketpds ./cmd/pocketpds

# CA certificates for HTTPS (PLC directory, SMTP, relays)
FROM alpine:3.20 AS certs
RUN apk --no-cache add ca-certificates

# Runtime
FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /pocketpds /pocketpds
EXPOSE 3000
VOLUME ["/data"]
ENV POCKETPDS_LISTEN=0.0.0.0:3000 \
    POCKETPDS_DB=/data/pocketpds.db \
    POCKETPDS_DATA_DIR=/data
ENTRYPOINT ["/pocketpds", "serve"]
