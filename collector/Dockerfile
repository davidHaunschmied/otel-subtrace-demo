FROM golang:1.21-alpine AS builder

RUN apk add --no-cache git make

# Install ocb (OpenTelemetry Collector Builder)
RUN go install go.opentelemetry.io/collector/cmd/builder@v0.88.0

WORKDIR /build

# Copy processor source
COPY processor/subtraceaggregator /build/processor/subtraceaggregator

# Initialize go.mod dependencies
WORKDIR /build/processor/subtraceaggregator
RUN go mod tidy

WORKDIR /build

# Copy builder config
COPY collector/builder-config.yaml /build/builder-config.yaml

# Build the collector
RUN builder --config=/build/builder-config.yaml

FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /build/otelcol-subtrace/otelcol-subtrace /otelcol-subtrace

EXPOSE 4317 4318 8888 8889 13133 55679

ENTRYPOINT ["/otelcol-subtrace"]
CMD ["--config=/etc/otel-collector-config.yaml"]
