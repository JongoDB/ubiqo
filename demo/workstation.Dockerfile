# A demo "machine": claude-code + the ubiqo CLI on a clean filesystem.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/ubiqo ./cmd/ubiqo

FROM node:22-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends git curl jq ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code
COPY --from=build /out/ubiqo /usr/local/bin/ubiqo
COPY demo/attach.sh /usr/local/bin/attach.sh
RUN chmod +x /usr/local/bin/attach.sh
WORKDIR /root/work
CMD ["sleep", "infinity"]
