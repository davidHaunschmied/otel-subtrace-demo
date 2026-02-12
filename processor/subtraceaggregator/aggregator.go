package subtraceaggregator

import (
	"regexp"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// Aggregator applies aggregation rules to a subtrace.
type Aggregator struct {
	attrAggs  []AttributeAggregation
	eventAggs []EventAggregation
}

// NewAggregator creates a new aggregator with the given rules.
func NewAggregator(attrAggs []AttributeAggregation, eventAggs []EventAggregation) *Aggregator {
	return &Aggregator{
		attrAggs:  attrAggs,
		eventAggs: eventAggs,
	}
}

// Apply applies all aggregations to the subtrace and enriches the root span.
func (a *Aggregator) Apply(state *SubtraceState) {
	if state.RootSpan == nil {
		return
	}

	// Apply attribute aggregations
	for _, agg := range a.attrAggs {
		a.applyAttributeAggregation(state, agg)
	}

	// Apply event aggregations
	for _, agg := range a.eventAggs {
		a.applyEventAggregation(state, agg)
	}
}

func (a *Aggregator) applyAttributeAggregation(state *SubtraceState, agg AttributeAggregation) {
	var values []pcommon.Value
	var count int

	for _, span := range state.Spans {
		// Skip root span for aggregation (we aggregate from children)
		if isRoot, ok := span.Attributes().Get("subtrace.is_root_span"); ok && isRoot.Bool() {
			continue
		}

		// Check condition
		if agg.Condition != "" && !evaluateSpanCondition(span, agg.Condition) {
			continue
		}

		count++

		// Get source value if needed
		if agg.Source != "" {
			if val := getAttributeValue(span, agg.Source); val.Type() != pcommon.ValueTypeEmpty {
				values = append(values, val)
			}
		}
	}

	// No matches - don't set attribute
	if count == 0 && agg.Aggregation != "count" {
		return
	}
	if len(values) == 0 && agg.Aggregation != "count" {
		return
	}

	// Apply aggregation and set on root span
	result := computeAggregation(agg.Aggregation, values, count, agg.MaxValues)
	if result.Type() != pcommon.ValueTypeEmpty {
		result.CopyTo(state.RootSpan.Attributes().PutEmpty(agg.Target))
	}
}

func (a *Aggregator) applyEventAggregation(state *SubtraceState, agg EventAggregation) {
	var matchingEvents []struct {
		event      ptrace.SpanEvent
		sourceSpan ptrace.Span
	}

	for _, span := range state.Spans {
		events := span.Events()
		for i := 0; i < events.Len(); i++ {
			event := events.At(i)
			if event.Name() != agg.Source {
				continue
			}

			// Check condition on event attributes
			if agg.Condition != "" && !evaluateEventCondition(event, agg.Condition) {
				continue
			}

			matchingEvents = append(matchingEvents, struct {
				event      ptrace.SpanEvent
				sourceSpan ptrace.Span
			}{event: event, sourceSpan: span})
		}
	}

	if len(matchingEvents) == 0 {
		return
	}

	switch agg.Aggregation {
	case "copy_event":
		maxEvents := agg.MaxEvents
		if maxEvents <= 0 {
			maxEvents = 10
		}
		for i, me := range matchingEvents {
			if i >= maxEvents {
				break
			}
			// Copy event to root span
			newEvent := state.RootSpan.Events().AppendEmpty()
			me.event.CopyTo(newEvent)
			// Add source_span_id
			newEvent.Attributes().PutStr("source_span_id", me.sourceSpan.SpanID().String())
		}

	case "count":
		state.RootSpan.Attributes().PutInt(agg.Target, int64(len(matchingEvents)))
	}
}

// evaluateSpanCondition evaluates a simple OTTL-like condition against a span.
// Supports: attributes["key"] != nil, attributes["key"] == "value", attributes["key"] == true
func evaluateSpanCondition(span ptrace.Span, condition string) bool {
	return evaluateCondition(span.Attributes(), condition)
}

// evaluateEventCondition evaluates a condition against an event.
func evaluateEventCondition(event ptrace.SpanEvent, condition string) bool {
	return evaluateCondition(event.Attributes(), condition)
}

// evaluateCondition evaluates a simple condition against attributes.
func evaluateCondition(attrs pcommon.Map, condition string) bool {
	// Handle AND conditions
	if strings.Contains(condition, " and ") {
		parts := strings.Split(condition, " and ")
		for _, part := range parts {
			if !evaluateSingleCondition(attrs, strings.TrimSpace(part)) {
				return false
			}
		}
		return true
	}

	// Handle OR conditions
	if strings.Contains(condition, " or ") {
		parts := strings.Split(condition, " or ")
		for _, part := range parts {
			if evaluateSingleCondition(attrs, strings.TrimSpace(part)) {
				return true
			}
		}
		return false
	}

	return evaluateSingleCondition(attrs, condition)
}

func evaluateSingleCondition(attrs pcommon.Map, condition string) bool {
	// Pattern: attributes["key"] != nil
	nilCheckPattern := regexp.MustCompile(`attributes\["([^"]+)"\]\s*!=\s*nil`)
	if matches := nilCheckPattern.FindStringSubmatch(condition); len(matches) == 2 {
		_, exists := attrs.Get(matches[1])
		return exists
	}

	// Pattern: attributes["key"] == nil
	nilEqPattern := regexp.MustCompile(`attributes\["([^"]+)"\]\s*==\s*nil`)
	if matches := nilEqPattern.FindStringSubmatch(condition); len(matches) == 2 {
		_, exists := attrs.Get(matches[1])
		return !exists
	}

	// Pattern: attributes["key"] == "value"
	strEqPattern := regexp.MustCompile(`attributes\["([^"]+)"\]\s*==\s*"([^"]*)"`)
	if matches := strEqPattern.FindStringSubmatch(condition); len(matches) == 3 {
		val, exists := attrs.Get(matches[1])
		return exists && val.Str() == matches[2]
	}

	// Pattern: attributes["key"] != "value"
	strNeqPattern := regexp.MustCompile(`attributes\["([^"]+)"\]\s*!=\s*"([^"]*)"`)
	if matches := strNeqPattern.FindStringSubmatch(condition); len(matches) == 3 {
		val, exists := attrs.Get(matches[1])
		return !exists || val.Str() != matches[2]
	}

	// Pattern: attributes["key"] == true/false
	boolPattern := regexp.MustCompile(`attributes\["([^"]+)"\]\s*==\s*(true|false)`)
	if matches := boolPattern.FindStringSubmatch(condition); len(matches) == 3 {
		val, exists := attrs.Get(matches[1])
		expected := matches[2] == "true"
		return exists && val.Bool() == expected
	}

	// Unknown condition - return true (permissive)
	return true
}

// getAttributeValue extracts an attribute value from a span using OTTL-like path.
func getAttributeValue(span ptrace.Span, source string) pcommon.Value {
	// Pattern: attributes["key"]
	attrPattern := regexp.MustCompile(`attributes\["([^"]+)"\]`)
	if matches := attrPattern.FindStringSubmatch(source); len(matches) == 2 {
		if val, exists := span.Attributes().Get(matches[1]); exists {
			return val
		}
	}
	return pcommon.NewValueEmpty()
}

// computeAggregation computes the aggregated value.
func computeAggregation(aggType string, values []pcommon.Value, count int, maxValues int) pcommon.Value {
	if maxValues <= 0 {
		maxValues = 100
	}

	switch aggType {
	case "count":
		result := pcommon.NewValueInt(int64(count))
		return result

	case "any":
		if len(values) > 0 {
			return values[0]
		}

	case "sum":
		var sum float64
		var isInt bool = true
		for _, v := range values {
			switch v.Type() {
			case pcommon.ValueTypeInt:
				sum += float64(v.Int())
			case pcommon.ValueTypeDouble:
				sum += v.Double()
				isInt = false
			}
		}
		if isInt {
			return pcommon.NewValueInt(int64(sum))
		}
		return pcommon.NewValueDouble(sum)

	case "avg":
		if len(values) == 0 {
			return pcommon.NewValueEmpty()
		}
		var sum float64
		for _, v := range values {
			switch v.Type() {
			case pcommon.ValueTypeInt:
				sum += float64(v.Int())
			case pcommon.ValueTypeDouble:
				sum += v.Double()
			}
		}
		return pcommon.NewValueDouble(sum / float64(len(values)))

	case "min":
		if len(values) == 0 {
			return pcommon.NewValueEmpty()
		}
		minVal := getNumericValue(values[0])
		for _, v := range values[1:] {
			if n := getNumericValue(v); n < minVal {
				minVal = n
			}
		}
		return pcommon.NewValueDouble(minVal)

	case "max":
		if len(values) == 0 {
			return pcommon.NewValueEmpty()
		}
		maxVal := getNumericValue(values[0])
		for _, v := range values[1:] {
			if n := getNumericValue(v); n > maxVal {
				maxVal = n
			}
		}
		return pcommon.NewValueDouble(maxVal)

	case "all":
		result := pcommon.NewValueSlice()
		slice := result.Slice()
		for i, v := range values {
			if i >= maxValues {
				break
			}
			v.CopyTo(slice.AppendEmpty())
		}
		return result

	case "all_distinct":
		seen := make(map[string]bool)
		result := pcommon.NewValueSlice()
		slice := result.Slice()
		for _, v := range values {
			key := v.AsString()
			if !seen[key] {
				seen[key] = true
				if slice.Len() >= maxValues {
					break
				}
				v.CopyTo(slice.AppendEmpty())
			}
		}
		return result
	}

	return pcommon.NewValueEmpty()
}

func getNumericValue(v pcommon.Value) float64 {
	switch v.Type() {
	case pcommon.ValueTypeInt:
		return float64(v.Int())
	case pcommon.ValueTypeDouble:
		return v.Double()
	}
	return 0
}
