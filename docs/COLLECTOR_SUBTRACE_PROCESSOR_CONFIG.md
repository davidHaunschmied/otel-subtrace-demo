# OTel Collector Stateful Subtrace Processor Configuration

## Overview

This document describes the configuration for a stateful subtrace processor in the OpenTelemetry Collector. The processor aggregates data from child spans onto the subtrace root span.

## Processor Configuration Schema

```yaml
processors:
  subtrace:
    # How long to wait for all spans in a subtrace before flushing
    timeout: 30s
    
    # Maximum spans to buffer per subtrace (memory protection)
    max_spans_per_subtrace: 1000
    
    # Maximum concurrent subtraces in memory
    max_subtraces: 10000
    
    # What to do when limits are reached
    eviction_policy: lru  # lru | oldest_first | drop_new
    
    # Aggregation rules
    aggregations:
      # Rule 1: Copy exception events to root span
      - name: "propagate_exceptions"
        type: copy_event
        source:
          event_name: "exception"
        target:
          event_name: "exception"
        copy_attributes:
          - "exception.type"
        add_attributes:
          - key: "exception.source_span_id"
            value: "${source_span_id}"
      
      # Rule 2: Count database calls
      - name: "count_db_calls"
        type: count
        source:
          attribute: "db.system"
          # Only count spans that have this attribute
        target:
          attribute: "subtrace.db_call_count"
      
      # Rule 3: First seen loyalty status
      - name: "capture_loyalty_status"
        type: first
        source:
          attribute: "customer.loyalty_status"
        target:
          attribute: "subtrace.customer.loyalty_status"
        value_type: string
```

## Aggregation Types

### 1. `first` - First Seen Value

Captures the first non-null value of an attribute seen in any child span.

```yaml
- name: "capture_loyalty_status"
  type: first
  source:
    attribute: "customer.loyalty_status"
  target:
    attribute: "subtrace.customer.loyalty_status"
  value_type: string  # string | int | double | bool
```

**Use Case:** Capture a value determined early in processing (e.g., customer tier from DB lookup).

### 2. `last` - Last Seen Value

Captures the last non-null value of an attribute.

```yaml
- name: "final_status"
  type: last
  source:
    attribute: "processing.status"
  target:
    attribute: "subtrace.final_status"
  value_type: string
```

### 3. `all` - All Values (Array)

Collects all values into an array, preserving duplicates.

```yaml
- name: "all_error_codes"
  type: all
  source:
    attribute: "error.code"
  target:
    attribute: "subtrace.error_codes"
  value_type: string  # Results in string[]
  max_values: 100  # Limit array size
```

### 4. `all_distinct` - All Unique Values

Collects unique values into an array.

```yaml
- name: "unique_tables_accessed"
  type: all_distinct
  source:
    attribute: "db.sql.table"
  target:
    attribute: "subtrace.tables_accessed"
  value_type: string
  max_values: 50
```

### 5. `count` - Count Occurrences

Counts spans that have a specific attribute (non-null).

```yaml
- name: "count_db_calls"
  type: count
  source:
    attribute: "db.system"
  target:
    attribute: "subtrace.db_call_count"
```

**Use Case:** N+1 query detection - count database calls per request.

### 6. `sum` - Sum Numeric Values

Sums numeric attribute values.

```yaml
- name: "total_bytes_processed"
  type: sum
  source:
    attribute: "data.bytes_processed"
  target:
    attribute: "subtrace.total_bytes"
  value_type: int  # int | double
```

### 7. `avg` - Average Numeric Values

Calculates average of numeric values.

```yaml
- name: "avg_query_time"
  type: avg
  source:
    attribute: "db.query.duration_ms"
  target:
    attribute: "subtrace.avg_query_time_ms"
  value_type: double
```

### 8. `min` / `max` - Minimum/Maximum Values

```yaml
- name: "slowest_query"
  type: max
  source:
    attribute: "db.query.duration_ms"
  target:
    attribute: "subtrace.max_query_time_ms"
  value_type: double
```

### 9. `copy_event` - Copy Span Events

Copies span events from child spans to the root span.

```yaml
- name: "propagate_exceptions"
  type: copy_event
  source:
    event_name: "exception"
    # Optional: filter by event attributes
    filter:
      - key: "exception.type"
        pattern: ".*Exception$"
  target:
    event_name: "exception"  # Can rename the event
  copy_attributes:
    - "exception.type"
    - "exception.message"  # Optional: include message
  add_attributes:
    - key: "exception.source_span_id"
      value: "${source_span_id}"
    - key: "exception.source_span_name"
      value: "${source_span_name}"
  max_events: 10  # Limit events copied
```

**Use Case:** Propagate business exceptions (PaymentFailedException) to root span for alerting.

## Complete Configuration Example

```yaml
# otel-collector-config.yaml

receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  # Standard processors
  memory_limiter:
    check_interval: 1s
    limit_mib: 512
    spike_limit_mib: 128

  batch:
    timeout: 1s
    send_batch_size: 1024

  # Stateful Subtrace Processor
  subtrace:
    timeout: 30s
    max_spans_per_subtrace: 500
    max_subtraces: 5000
    
    aggregations:
      # Use Case 1: Exception Propagation
      - name: "propagate_payment_exceptions"
        type: copy_event
        source:
          event_name: "exception"
        target:
          event_name: "exception"
        copy_attributes:
          - "exception.type"
        add_attributes:
          - key: "exception.source_span_id"
            value: "${source_span_id}"
        max_events: 5
      
      # Use Case 2: Database Call Counting
      - name: "count_database_calls"
        type: count
        source:
          attribute: "db.system"
        target:
          attribute: "subtrace.db_call_count"
      
      # Use Case 2b: List tables accessed
      - name: "tables_accessed"
        type: all_distinct
        source:
          attribute: "db.sql.table"
        target:
          attribute: "subtrace.tables_accessed"
        value_type: string
        max_values: 20
      
      # Use Case 3: Loyalty Status
      - name: "capture_loyalty"
        type: first
        source:
          attribute: "customer.loyalty_status"
        target:
          attribute: "subtrace.customer.loyalty_status"
        value_type: string
      
      # Additional useful aggregations
      - name: "has_errors"
        type: count
        source:
          attribute: "error"
          value: true  # Only count when error=true
        target:
          attribute: "subtrace.error_count"
      
      - name: "total_duration"
        type: sum
        source:
          attribute: "duration_ms"
        target:
          attribute: "subtrace.total_child_duration_ms"
        value_type: double

exporters:
  otlp/jaeger:
    endpoint: jaeger:4317
    tls:
      insecure: true

  zipkin:
    endpoint: "http://zipkin:9411/api/v2/spans"

  debug:
    verbosity: detailed

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, subtrace, batch]
      exporters: [debug, otlp/jaeger, zipkin]
```

## How the Processor Works

### Span Identification

The processor identifies subtraces using two span attributes:
- `subtrace.id`: Unique identifier for the subtrace (set by application)
- `subtrace.is_root_span`: Boolean indicating the root span

### Processing Flow

```
1. Span arrives at processor
   │
   ├─► Extract subtrace.id
   │
   ├─► If subtrace.id not in buffer:
   │       Create new subtrace buffer
   │
   ├─► Add span to subtrace buffer
   │
   ├─► If subtrace.is_root_span == true:
   │       Mark this span as root for aggregation
   │
   └─► Check completion conditions:
       │
       ├─► Root span ended? → Apply aggregations, emit all spans
       │
       ├─► Timeout reached? → Apply aggregations, emit all spans
       │
       └─► Max spans reached? → Apply aggregations, emit all spans
```

### Aggregation Application

When a subtrace completes:

1. Find the root span (where `subtrace.is_root_span == true`)
2. For each aggregation rule:
   - Scan all child spans for source attribute/event
   - Compute aggregated value
   - Add result to root span attributes/events
3. Emit all spans (root span now enriched)

## Metrics Emitted

The processor emits these metrics for monitoring:

| Metric | Type | Description |
|--------|------|-------------|
| `subtrace_processor_active_subtraces` | Gauge | Subtraces currently buffered |
| `subtrace_processor_buffered_spans` | Gauge | Total spans in buffer |
| `subtrace_processor_completed_total` | Counter | Subtraces completed normally |
| `subtrace_processor_timeout_total` | Counter | Subtraces flushed due to timeout |
| `subtrace_processor_evicted_total` | Counter | Subtraces evicted (memory) |
| `subtrace_processor_aggregation_errors_total` | Counter | Aggregation failures |

## Implementation Notes

### Current Status

**This processor does not exist yet in the OpenTelemetry Collector.**

The configuration above is a design specification. Implementation options:

1. **Custom Processor (Go)**: Implement as a collector-contrib processor
2. **Transform Processor Approximation**: Limited aggregation using existing transform processor
3. **External Processing**: Export to a service that performs aggregation

### Transform Processor Approximation

For simple use cases, the transform processor can add attributes based on conditions:

```yaml
processors:
  transform:
    trace_statements:
      - context: span
        statements:
          # Mark spans with exceptions
          - set(attributes["has_exception"], true) where IsMatch(name, ".*exception.*")
```

However, this cannot:
- Aggregate across multiple spans
- Copy data from child to parent spans
- Perform stateful operations

### Recommended Implementation Path

1. **Phase 1**: Implement basic buffering and `first`/`count` aggregations
2. **Phase 2**: Add `copy_event` for exception propagation
3. **Phase 3**: Add numeric aggregations (`sum`, `avg`, `min`, `max`)
4. **Phase 4**: Add array aggregations (`all`, `all_distinct`)

## Testing the Configuration

Once implemented, test with:

```bash
# Make requests to generate traces
curl http://localhost:8001/api/process/user123

# Check Jaeger/Zipkin for enriched root spans
# Root span should have:
#   - subtrace.db_call_count: <number>
#   - subtrace.customer.loyalty_status: "gold"
#   - exception event (if payment failed)
```

## Related Documents

- [Architecture Overview](./ARCHITECTURE_STATEFUL_SUBTRACE_PROCESSOR.md)
- [SubtraceIdProcessor (Python)](../subtrace_processor.py)
- [Service A Implementation](../service_a_new.py)
- [Service B Implementation](../service_b_new.py)
