package subtraceaggregator

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config defines the configuration for the subtraceaggregator processor.
type Config struct {
	Timeout               time.Duration          `mapstructure:"timeout"`
	MaxSpansPerSubtrace   int                    `mapstructure:"max_spans_per_subtrace"`
	AttributeAggregations []AttributeAggregation `mapstructure:"attribute_aggregations"`
	EventAggregations     []EventAggregation     `mapstructure:"event_aggregations"`
}

type AttributeAggregation struct {
	Aggregation string `mapstructure:"aggregation"`
	Source      string `mapstructure:"source"`
	Condition   string `mapstructure:"condition"`
	Target      string `mapstructure:"target"`
	MaxValues   int    `mapstructure:"max_values"`
}

type EventAggregation struct {
	Aggregation string `mapstructure:"aggregation"`
	Source      string `mapstructure:"source"`
	Condition   string `mapstructure:"condition"`
	Target      string `mapstructure:"target"`
	MaxEvents   int    `mapstructure:"max_events"`
}

var _ component.Config = (*Config)(nil)

func (cfg *Config) Validate() error {
	if cfg.Timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if cfg.MaxSpansPerSubtrace <= 0 {
		return errors.New("max_spans_per_subtrace must be positive")
	}
	for _, agg := range cfg.AttributeAggregations {
		if err := validateAttributeAggregation(agg); err != nil {
			return err
		}
	}
	for _, agg := range cfg.EventAggregations {
		if err := validateEventAggregation(agg); err != nil {
			return err
		}
	}
	return nil
}

func validateAttributeAggregation(agg AttributeAggregation) error {
	validTypes := map[string]bool{
		"count": true, "sum": true, "any": true, "min": true,
		"max": true, "avg": true, "all": true, "all_distinct": true,
	}
	if !validTypes[agg.Aggregation] {
		return errors.New("invalid aggregation type in attribute_aggregations")
	}
	if agg.Target == "" {
		return errors.New("target is required in attribute_aggregations")
	}
	if agg.Aggregation != "count" && agg.Source == "" {
		return errors.New("source is required for non-count aggregations")
	}
	return nil
}

func validateEventAggregation(agg EventAggregation) error {
	if agg.Aggregation != "copy_event" && agg.Aggregation != "count" {
		return errors.New("event aggregation must be copy_event or count")
	}
	if agg.Source == "" {
		return errors.New("source (event name) is required in event_aggregations")
	}
	if agg.Aggregation == "count" && agg.Target == "" {
		return errors.New("target is required for count event aggregation")
	}
	return nil
}
