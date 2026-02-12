package subtraceaggregator

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

const (
	typeStr   = "subtraceaggregator"
	stability = component.StabilityLevelDevelopment
)

// NewFactory creates a factory for the subtraceaggregator processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		typeStr,
		createDefaultConfig,
		processor.WithTraces(createTracesProcessor, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		Timeout:             30 * time.Second,
		MaxSpansPerSubtrace: 1000,
		ErrorMode:           "ignore",
	}
}

func createTracesProcessor(
	ctx context.Context,
	set processor.CreateSettings,
	cfg component.Config,
	nextConsumer consumer.Traces,
) (processor.Traces, error) {
	processorCfg := cfg.(*Config)
	return newProcessor(set.Logger, processorCfg, nextConsumer)
}
