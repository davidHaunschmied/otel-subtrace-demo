# Subtrace Architecture

## Overview

This document describes the architecture for the **Subtrace pattern** - a technique for service-level real-time analytics using OpenTelemetry.

A **subtrace** is a logical grouping of all spans within a single service for a given trace. By aggregating child span data onto the subtrace root span, we enable:
- Business-level failure detection even when requests technically succeed
- N+1 query detection and database call counting
- Deep analytics by propagating child span data to root spans

> **See also:** [Subtrace Aggregator Processor Spec](./SUBTRACEAGGREGATOR_PROCESSOR_SPEC.md) for the complete configuration reference.

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           APPLICATION LAYER                                  │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                    SubtraceIdProcessor (Python)                      │   │
│  │                                                                      │   │
│  │  On first span in trace context:                                     │   │
│  │    1. Generate subtrace.id = hash(trace_id + span_id)               │   │
│  │    2. Set subtrace.is_root_span = true                              │   │
│  │    3. Store subtrace.id in context                                  │   │
│  │                                                                      │   │
│  │  On subsequent spans:                                                │   │
│  │    1. Read subtrace.id from context                                 │   │
│  │    2. Set subtrace.id on span                                       │   │
│  │    3. Set subtrace.is_root_span = false                             │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                         OTLP Exporter                                │   │
│  │                    Sends spans to collector                          │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
                                     │
                                     ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         OTEL COLLECTOR LAYER                                 │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │              subtraceaggregator Processor (Collector)               │   │
│  │                                                                      │   │
│  │  State Management:                                                   │   │
│  │    - Buffer spans by subtrace.id                                    │   │
│  │    - Track root span reference per subtrace                         │   │
│  │    - Apply aggregations when subtrace completes                     │   │
│  │                                                                      │   │
│  │  Aggregation Types:                                                  │   │
│  │    - any: First encountered value (arrival order)                   │   │
│  │    - all_distinct: All unique values (array)                        │   │
│  │    - all: All values (array)                                        │   │
│  │    - sum: Sum of numeric values                                     │   │
│  │    - avg: Average of numeric values                                 │   │
│  │    - max: Maximum numeric value                                     │   │
│  │    - min: Minimum numeric value                                     │   │
│  │    - count: Count of occurrences                                    │   │
│  │    - copy_event: Copy span events to root span                      │   │
│  │                                                                      │   │
│  │  Completion Detection:                                               │   │
│  │    - Timeout (configurable, default 30s)                            │   │
│  │    - Max spans per subtrace (memory protection)                     │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                         Exporters                                    │   │
│  │                  (Jaeger, Zipkin, OTLP, etc.)                        │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Component Details

### 1. Application-Side: SubtraceIdProcessor (Python)

**Location:** Runs as a SpanProcessor in each service

**Responsibilities:**
- Generate unique subtrace ID for each service invocation
- Mark root spans with `subtrace.is_root_span = true`
- Propagate subtrace ID to all child spans via OpenTelemetry context

**Span Attributes Set:**
| Attribute | Type | Description |
|-----------|------|-------------|
| `subtrace.id` | string (hex) | 64-bit unique ID for the subtrace |
| `subtrace.is_root_span` | boolean | `true` for service entry span |

**Implementation:**
```python
class SubtraceIdProcessor(SpanProcessor):
    """
    Assigns subtrace IDs to spans within a service.
    
    A subtrace groups all spans within a service for a given trace.
    The subtrace ID is a 64-bit hash of (trace_id + first_span_id).
    """
    
    SUBTRACE_CONTEXT_KEY = create_key("subtrace_id")
    
    def on_start(self, span: Span, parent_context: Context) -> None:
        # Check if subtrace ID exists in context
        subtrace_id = get_value(self.SUBTRACE_CONTEXT_KEY, parent_context)
        
        if subtrace_id is None:
            # First span in this service - generate subtrace ID
            subtrace_id = self._generate_subtrace_id(
                span.get_span_context().trace_id,
                span.get_span_context().span_id
            )
            span.set_attribute("subtrace.is_root_span", True)
        else:
            span.set_attribute("subtrace.is_root_span", False)
        
        span.set_attribute("subtrace.id", subtrace_id)
        
        # Store in context for child spans
        attach(set_value(self.SUBTRACE_CONTEXT_KEY, subtrace_id))
    
    def _generate_subtrace_id(self, trace_id: int, span_id: int) -> str:
        # Create 64-bit hash from trace_id and span_id
        combined = f"{trace_id:032x}{span_id:016x}"
        hash_bytes = hashlib.sha256(combined.encode()).digest()[:8]
        return hash_bytes.hex()
```

### 2. Collector-Side: subtraceaggregator Processor

**Location:** OpenTelemetry Collector processor

**Responsibilities:**
- Buffer spans by `subtrace.id`
- Track root span (where `subtrace.is_root_span == true`)
- Apply configured aggregations using OTTL conditions
- Emit enriched spans when subtrace completes (timeout or max spans)

**Configuration Schema:**
```yaml
processors:
  subtraceaggregator:
    timeout: 30s
    max_spans_per_subtrace: 1000
    error_mode: ignore
    
    attribute_aggregations:
      # Count database calls (N+1 detection)
      - aggregation: count
        condition: 'attributes["db.system"] != nil'
        target: subtrace.db_call_count
      
      # Sum pre-aggregated counts
      - aggregation: sum
        source: attributes["aggregation.count"]
        condition: 'attributes["db.system"] != nil'
        target: subtrace.db_call_count
      
      # Capture customer tier (first encountered)
      - aggregation: any
        source: attributes["customer.loyalty_status"]
        target: subtrace.customer.loyalty_status
    
    event_aggregations:
      # Propagate payment exceptions to root span
      - aggregation: copy_event
        source: exception
        condition: 'attributes["exception.type"] == "PaymentFailedException"'
        max_events: 5
```

**Aggregation Types:**

| Aggregation | Input Type | Output Type | Description |
|-------------|------------|-------------|-------------|
| `any` | any | same | First encountered value (arrival order) |
| `all` | any | array | All values (preserves duplicates) |
| `all_distinct` | any | array | All unique values |
| `count` | — | int | Count of matching spans/events |
| `sum` | numeric | same | Sum of values |
| `avg` | numeric | double | Average of values |
| `min` | numeric | same | Minimum value |
| `max` | numeric | same | Maximum value |
| `copy_event` | event | event | Copy span events to root |

> **Full specification:** [SUBTRACEAGGREGATOR_PROCESSOR_SPEC.md](./SUBTRACEAGGREGATOR_PROCESSOR_SPEC.md)

### 3. Data Flow

```
1. Request enters Service A
   └─► SubtraceIdProcessor generates subtrace.id = "abc123"
   └─► Root span: subtrace.is_root_span = true, subtrace.id = "abc123"
   
2. Service A creates child spans
   └─► Child spans: subtrace.is_root_span = false, subtrace.id = "abc123"
   └─► One child has exception event
   └─► Another child has db.system = "postgresql"
   
3. Spans exported to Collector
   └─► Collector buffers spans by subtrace.id
   
4. Root span ends (or timeout)
   └─► Collector applies aggregations:
       - Copies exception event to root span
       - Sets subtrace.db_call_count = 3
       - Sets subtrace.customer.loyalty_status = "gold"
   └─► Emits all spans with enriched root span
```

## Use Cases Implementation

### Use Case 1: Exception Propagation

**Scenario:** PaymentFailedException occurs in a deep child span, but the request returns 200 OK.

**Application Side:**
```python
# In payment processing code
try:
    payment_result = payment_service.process(payment)
except PaymentFailedException as e:
    # Record exception as span event
    current_span = trace.get_current_span()
    current_span.record_exception(e)
    # Handle gracefully, return success with fallback
    return {"status": "pending", "message": "Payment queued"}
```

**Collector Aggregation:**
```yaml
event_aggregations:
  - aggregation: copy_event
    source: exception
    condition: 'attributes["exception.type"] == "PaymentFailedException"'
    max_events: 5
```

**Result on Root Span:**
```
Events:
  - name: "exception"
    attributes:
      exception.type: "PaymentFailedException"
      exception.message: "Payment declined"
      source_span_id: "abc123def456"
```

### Use Case 2: Database Call Counting (N+1 Detection)

**Scenario:** Service makes multiple database calls; count them for N+1 analysis.

**Application Side:**
```python
# Database calls automatically instrumented with db.system attribute
# Or manually:
with tracer.start_as_current_span("db-query") as span:
    span.set_attribute("db.system", "postgresql")
    span.set_attribute("db.operation", "SELECT")
    result = db.execute(query)
```

**Collector Aggregation (count spans):**
```yaml
attribute_aggregations:
  - aggregation: count
    condition: 'attributes["db.system"] != nil'
    target: subtrace.db_call_count
```

**Collector Aggregation (sum pre-aggregated counts):**
```yaml
attribute_aggregations:
  - aggregation: sum
    source: attributes["aggregation.count"]
    condition: 'attributes["db.system"] != nil'
    target: subtrace.db_call_count
```

**Result on Root Span:**
```
Attributes:
  subtrace.db_call_count: 7
```

### Use Case 3: Loyalty Status Propagation

**Scenario:** Customer loyalty status determined in child span, needed on root for metrics.

**Application Side:**
```python
# In customer lookup code
with tracer.start_as_current_span("lookup-customer") as span:
    customer = db.get_customer(customer_id)
    span.set_attribute("customer.loyalty_status", customer.loyalty_status)
    span.set_attribute("customer.id", customer_id)
```

**Collector Aggregation:**
```yaml
attribute_aggregations:
  - aggregation: any
    source: attributes["customer.loyalty_status"]
    target: subtrace.customer.loyalty_status
```

**Result on Root Span:**
```
Attributes:
  subtrace.customer.loyalty_status: "gold"
```

## Memory Management

The stateful processor must handle memory carefully:

1. **Timeout:** Incomplete subtraces are flushed after configurable timeout
2. **Max Spans:** Limit spans buffered per subtrace

```yaml
processors:
  subtraceaggregator:
    timeout: 30s
    max_spans_per_subtrace: 1000
```

## Span Ordering Considerations

Spans may arrive out of order. The `any` aggregation uses **arrival order** (not chronological). This is appropriate when:
- All values are expected to be the same
- Only one span will have the attribute

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Root span never arrives | Flush on timeout, spans pass through unchanged |
| No matching spans | Target attribute not set on root span |
| Type mismatch | Span skipped for that aggregation |
| Invalid condition | Log warning, skip aggregation |

## Metrics Emitted by Processor

| Metric | Type | Description |
|--------|------|-------------|
| `subtraceaggregator_active_subtraces` | Gauge | Current subtraces in memory |
| `subtraceaggregator_buffered_spans` | Gauge | Total spans buffered |
| `subtraceaggregator_completed_total` | Counter | Subtraces completed |
| `subtraceaggregator_no_root_span_total` | Counter | Subtraces without root span |
| `subtraceaggregator_aggregation_errors_total` | Counter | Aggregation failures |

## Related Documents

- [Subtrace Aggregator Processor Spec](./SUBTRACEAGGREGATOR_PROCESSOR_SPEC.md) — Complete configuration reference
- [SubtraceIdProcessor (Python)](../subtrace_processor.py) — Application-side processor
