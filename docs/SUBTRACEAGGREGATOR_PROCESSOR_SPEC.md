# Subtrace Aggregator Processor Specification

## Overview

The `subtraceaggregator` processor aggregates data from child spans onto the subtrace root span, enabling service-level real-time analytics. It buffers spans by `subtrace.id`, applies configured aggregations when the subtrace completes, and emits enriched spans.

## Status

| Status | |
|--------|---|
| Stability | Design Phase |
| Language | Go (OpenTelemetry Collector) |

## Concepts

### Subtrace
A **subtrace** is a logical grouping of all spans within a single service for a given distributed trace. Each subtrace has:
- `subtrace.id`: Unique identifier (set by application-side `SubtraceIdProcessor`)
- `subtrace.is_root_span`: Boolean marking the service entry point span

### Aggregation
The processor collects data from child spans and aggregates it onto the root span, enabling queries like:
- "How many DB calls did this request make?" (N+1 detection)
- "Did any child span have a PaymentFailedException?"
- "What was the customer's loyalty status?"

## Configuration

### General Config

```yaml
processors:
  subtraceaggregator:
    # How long to wait for all spans in a subtrace before flushing
    timeout: 30s
    
    # Maximum spans to buffer per subtrace (memory protection)
    max_spans_per_subtrace: 1000
    
    # Error handling mode
    error_mode: ignore  # ignore | silent | propagate
    
    # Aggregation rules by context
    attribute_aggregations: []
    event_aggregations: []
```

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `timeout` | duration | `30s` | Time to wait for subtrace completion after first span arrives |
| `max_spans_per_subtrace` | int | `1000` | Maximum spans buffered per subtrace |
| `error_mode` | string | `ignore` | How to handle errors: `ignore`, `silent`, `propagate` |
| `attribute_aggregations` | list | `[]` | Aggregations on span attributes |
| `event_aggregations` | list | `[]` | Aggregations on span events |

### Error Modes

| Mode | Behavior |
|------|----------|
| `ignore` | Log error, continue processing other aggregations |
| `silent` | Skip silently, no logging |
| `propagate` | Fail the pipeline, drop the payload |

---

## Attribute Aggregations

Aggregate span attribute values onto the root span.

### Schema

```yaml
attribute_aggregations:
  - aggregation: <aggregation_type>
    source: <OTTL_path>           # Optional for 'count'
    condition: <OTTL_expression>  # Optional
    target: <attribute_name>
    max_values: <int>             # Only for 'all', 'all_distinct'
```

### Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `aggregation` | string | Yes | Aggregation function (see below) |
| `source` | string | No* | OTTL path to source attribute. *Required except for `count` |
| `condition` | string | No | OTTL boolean expression to filter spans |
| `target` | string | Yes | Target attribute name on root span |
| `max_values` | int | No | Max array size for `all`/`all_distinct` (default: 100) |

### Aggregation Types

| Type | Input | Output | Description |
|------|-------|--------|-------------|
| `sum` | numeric | numeric | Sum of values across matching spans |
| `count` | — | int | Count of matching spans |
| `any` | any | same | First encountered non-null value (arrival order) |
| `min` | numeric | numeric | Minimum value |
| `max` | numeric | numeric | Maximum value |
| `avg` | numeric | float | Average of values |
| `all` | any | array | All values (preserves duplicates) |
| `all_distinct` | any | array | Unique values only |

### Behavior

- **No matches**: Target attribute is **not set** on root span
- **Type mismatch**: Span is **skipped** for that aggregation
- **Null source**: Span is **skipped** (null values not aggregated)

### Examples

#### Count Database Calls (N+1 Detection)

```yaml
attribute_aggregations:
  - aggregation: count
    condition: 'attributes["db.system"] != nil'
    target: subtrace.db_call_count
```

#### Sum Aggregated Counts

When child spans already have a count (e.g., `aggregation.count` = number of DB calls in that span):

```yaml
attribute_aggregations:
  - aggregation: sum
    source: attributes["aggregation.count"]
    condition: 'attributes["db.system"] != nil'
    target: subtrace.db_call_count
```

#### Capture Customer Loyalty Status

```yaml
attribute_aggregations:
  - aggregation: any
    source: attributes["customer.loyalty_status"]
    target: subtrace.customer.loyalty_status
```

#### Collect All Accessed Tables

```yaml
attribute_aggregations:
  - aggregation: all_distinct
    source: attributes["db.sql.table"]
    target: subtrace.tables_accessed
    max_values: 50
```

#### Max Query Duration

```yaml
attribute_aggregations:
  - aggregation: max
    source: attributes["db.query.duration_ms"]
    condition: 'attributes["db.system"] != nil'
    target: subtrace.max_query_duration_ms
```

---

## Event Aggregations

Aggregate span events onto the root span.

### Schema

```yaml
event_aggregations:
  - aggregation: <aggregation_type>
    source: <event_name>
    condition: <OTTL_expression>  # Optional
    target: <attribute_name>      # For count/sum/etc.
    max_events: <int>             # For copy_event
```

### Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `aggregation` | string | Yes | `copy_event`, `count`, or other aggregation type |
| `source` | string | Yes | Event name to match (e.g., `"exception"`) |
| `condition` | string | No | OTTL expression filtering events by their attributes |
| `target` | string | Yes* | Target attribute name. *Not used for `copy_event` |
| `max_events` | int | No | Max events to copy for `copy_event` (default: 10) |

### Aggregation Types for Events

| Type | Description |
|------|-------------|
| `copy_event` | Copy matching events to root span |
| `count` | Count matching events |
| `any`, `sum`, `min`, `max`, `avg` | Aggregate on event attribute (requires `source_attribute`) |
| `all`, `all_distinct` | Collect event attribute values |

### Event Condition Context

When writing conditions for events, use `attributes["key"]` to reference event attributes:

```yaml
condition: 'attributes["exception.type"] == "PaymentFailedException"'
```

The condition filters **which events** match, not which spans.

### Copy Event Behavior

When `aggregation: copy_event`:
- The **entire event** is copied to the root span (all attributes preserved)
- An additional attribute `source_span_id` is added to each copied event
- Events are copied in arrival order up to `max_events`

### Examples

#### Copy Payment Failure Exceptions

```yaml
event_aggregations:
  - aggregation: copy_event
    source: exception
    condition: 'attributes["exception.type"] == "PaymentFailedException"'
    max_events: 5
```

Result on root span:
```
Events:
  - name: "exception"
    attributes:
      exception.type: "PaymentFailedException"
      exception.message: "Payment declined"
      source_span_id: "abc123def456"
```

#### Count All Exceptions

```yaml
event_aggregations:
  - aggregation: count
    source: exception
    target: subtrace.exception_count
```

#### Count Specific Exception Type

```yaml
event_aggregations:
  - aggregation: count
    source: exception
    condition: 'attributes["exception.type"] == "PaymentFailedException"'
    target: subtrace.payment_failure_count
```

---

## Complete Configuration Example

```yaml
processors:
  subtraceaggregator:
    timeout: 30s
    max_spans_per_subtrace: 500
    error_mode: ignore
    
    attribute_aggregations:
      # N+1 Query Detection: Sum pre-aggregated counts
      - aggregation: sum
        source: attributes["aggregation.count"]
        condition: 'attributes["db.system"] != nil'
        target: subtrace.db_call_count
      
      # Capture customer tier (first encountered)
      - aggregation: any
        source: attributes["customer.loyalty_status"]
        target: subtrace.customer.loyalty_status
      
      # Collect unique tables accessed
      - aggregation: all_distinct
        source: attributes["db.sql.table"]
        target: subtrace.tables_accessed
        max_values: 20
      
      # Track slowest query
      - aggregation: max
        source: attributes["db.query.duration_ms"]
        target: subtrace.max_query_duration_ms
      
      # Count error spans
      - aggregation: count
        condition: 'attributes["error"] == true'
        target: subtrace.error_count
    
    event_aggregations:
      # Propagate payment failures to root span
      - aggregation: copy_event
        source: exception
        condition: 'attributes["exception.type"] == "PaymentFailedException"'
        max_events: 5
      
      # Count all exceptions
      - aggregation: count
        source: exception
        target: subtrace.exception_count

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, subtraceaggregator, batch]
      exporters: [otlp/jaeger, otlphttp/dynatrace]
```

---

## Processing Flow

```
1. Span arrives at processor
   │
   ├─► Extract subtrace.id from span attributes
   │   └─► If missing, pass through unchanged
   │
   ├─► If subtrace.id not in buffer:
   │       Create new subtrace buffer, start timeout timer
   │
   ├─► Add span to subtrace buffer
   │
   ├─► If subtrace.is_root_span == true:
   │       Mark this span as root for aggregation target
   │
   └─► Check completion conditions:
       │
       ├─► Timeout reached?
       │       └─► Apply aggregations, emit all spans
       │
       └─► Max spans reached?
               └─► Apply aggregations, emit all spans
```

### Aggregation Application

When a subtrace completes:

1. Find the root span (where `subtrace.is_root_span == true`)
2. If no root span found, emit spans unchanged (log warning)
3. For each aggregation rule:
   - Scan child spans/events matching condition
   - Compute aggregated value
   - If result exists, add to root span attributes/events
4. Emit all spans (root span now enriched)

---

## OTTL Expression Reference

Conditions use [OTTL (OpenTelemetry Transformation Language)](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/pkg/ottl) syntax.

### Paths

| Context | Path | Description |
|---------|------|-------------|
| Span | `attributes["key"]` | Span attribute |
| Span | `name` | Span name |
| Span | `kind` | Span kind |
| Span | `status.code` | Span status code |
| Event | `attributes["key"]` | Event attribute |
| Event | `name` | Event name |

### Operators

| Operator | Example |
|----------|---------|
| `==` | `attributes["db.system"] == "postgresql"` |
| `!=` | `attributes["status"] != "OK"` |
| `!= nil` | `attributes["db.system"] != nil` |
| `and` | `attributes["db.system"] != nil and attributes["db.operation"] == "SELECT"` |
| `or` | `attributes["error"] == true or status.code == 2` |
| `>`, `<`, `>=`, `<=` | `attributes["http.status_code"] >= 400` |

### Functions

Standard OTTL functions are available:
- `IsMatch(value, pattern)` — Regex match
- `Concat(values...)` — String concatenation
- See [OTTL Functions](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/pkg/ottl/ottlfuncs)

---

## Metrics Emitted

The processor emits these metrics for monitoring:

| Metric | Type | Description |
|--------|------|-------------|
| `subtraceaggregator_active_subtraces` | Gauge | Subtraces currently buffered |
| `subtraceaggregator_buffered_spans` | Gauge | Total spans in buffer |
| `subtraceaggregator_completed_total` | Counter | Subtraces completed (timeout or max spans) |
| `subtraceaggregator_no_root_span_total` | Counter | Subtraces without root span |
| `subtraceaggregator_aggregation_errors_total` | Counter | Aggregation failures |

---

## Memory Management

| Config | Purpose |
|--------|---------|
| `timeout` | Flush incomplete subtraces after duration |
| `max_spans_per_subtrace` | Limit memory per subtrace |

When limits are reached, the subtrace is flushed with whatever aggregations are possible.

---

## Limitations

1. **Stateful**: Requires memory to buffer spans; not suitable for extremely high cardinality
2. **Ordering**: `any` aggregation uses arrival order, not chronological order
3. **Root span required**: Aggregations target the root span; if missing, spans pass through unchanged
4. **Single collector**: State is not shared across collector instances (use sticky routing for distributed deployments)

---

## Related Documents

- [SubtraceIdProcessor (Python)](../subtrace_processor.py) — Application-side processor
- [Architecture Overview](./ARCHITECTURE_STATEFUL_SUBTRACE_PROCESSOR.md) — System design
