# Build
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=0.1.0-dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=${VERSION}" -o /out/ubiqo ./cmd/ubiqo

# Run — git is required (artifact plane, ADR-0002); pg_dump for `ubiqo backup`.
FROM alpine:3.20
RUN apk add --no-cache git postgresql16-client ca-certificates tzdata \
    && adduser -D -H -u 10001 ubiqo \
    && mkdir -p /var/lib/ubiqo && chown ubiqo /var/lib/ubiqo
COPY --from=build /out/ubiqo /usr/local/bin/ubiqo
USER ubiqo
ENV UBIQO_DATA_DIR=/var/lib/ubiqo
EXPOSE 8383
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8383/healthz || exit 1
ENTRYPOINT ["ubiqo"]
CMD ["serve"]
