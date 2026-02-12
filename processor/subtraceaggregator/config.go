package subtraceaggregator

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config defines the configuration for the subtraceaggregator processor.
type Config struct {
	// Timeout is how long to wait for subtrace completion after first span arrives.
	Timeout time.Duration `mapstructure:"timeout"`

	// MaxSpansPerSubtrace limits memory usage per subtrace.
	MaxSpansPerSubtrace int `mapstructure:"max_spans_per_subtrace"`

	// ErrorMode determines how errors are handled: ignore, silent, propagate.
	ErrorMode string `mapstructure:"error_mode"`

	// AttributeAggregations defines aggregations on span attributes.
	AttributeAggregations []AttributeAggregation `mapstructure:"attribute_aggregations"`

	// EventAggregations defines aggregations on span events.
	EventAggregations []EventAggregation `mapstructure:"event_aggregations"`
}

// AttributeAggregation defines an aggregation rule for span attributes.
type AttributeAggregation struct {
	// Aggregation type: count, sum, any, min, max, avg, all, all_distinct
	Aggregation string `mapstructure:"aggregation"`

	// Source is the OTTL path to the source attribute (optional for count).
	Source string `mapstructure:"source"`

	// Condition is an OTTL boolean expression to filter spans.
	Condition string `mapstructure:"condition"`

	// Target is the attribute name to set on the root span.
	Target string `mapstructure:"target"`

	// MaxValues limits array size for all/all_distinct (default: 100).
	MaxValues int `mapstructure:"max_values"`
}

// EventAggregation defines an aggregation rule for span events.
type EventAggregation struct {
	// Aggregation type: copy_event, count
	Aggregation string `mapstructure:"aggregation"`

	// Source is the event name to match.
	Source string `mapstructure:"source"`

	// Condition is an OTTL boolean expression to filter events.
	Condition string `mapstructure:"condition"`

	// Target is the attribute name for count aggregation.
	Target string `mapstructure:"target"`

	// MaxEvents limits copied events for copy_event (default: 10).
	MaxEvents int `mapstructure:"max_events"`
}

var _ component.Config = (*Config)(nil)

// Validate checks if the configuration is valid.
func (cfg *Config) Validate() error {
	if cfg.Timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if cfg.MaxSpansPerSubtrace <= 0 {
		return errors.New("max_spans_per_subtrace must be positive")
	}
	if cfg.ErrorMode != "" && cfg.ErrorMode != "ignore" && cfg.ErrorMode != "silent" && cfg.ErrorMode != "propagate" {
		return errors.New("error_mode must be one of: ignore, silent, propagate")
	}

	for i, agg := range cfg.AttributeAggregations {
		if err := validateAttributeAggregation(agg, i); err != nil {
			return err
		}
	}

	for i, agg := range cfg.EventAggregations {
		if err := validateEventAggregation(agg, i); err != nil {
			return err
		}
	}

	return nil
}

func validateAttributeAggregation(agg AttributeAggregation, index int) error {
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

func validateEventAggregation(agg EventAggregation, index int) error {
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
