module github.com/davidHaunschmied/otel-subtrace-demo/processor/subtraceaggregator

go 1.21

require (
	go.opentelemetry.io/collector/component v0.88.0
	go.opentelemetry.io/collector/consumer v0.88.0
	go.opentelemetry.io/collector/pdata v1.0.0-rcv0018
	go.opentelemetry.io/collector/processor v0.88.0
	go.uber.org/zap v1.26.0
)
