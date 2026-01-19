# Stateful Subtrace Processor Architecture

## Overview

This document describes the architecture for stateful span processing within a **subtrace** - a logical grouping of all spans within a service for a given trace.

The goal is to aggregate data from child spans onto the subtrace root span (service entry span), enabling:
- Business-level failure detection even when requests technically succeed
- N+1 query detection and database call counting
- Deep analytics by propagating child span data to root spans

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
│  │              Stateful Subtrace Processor (Collector)                 │   │
│  │                                                                      │   │
│  │  State Management:                                                   │   │
│  │    - Buffer spans by subtrace.id                                    │   │
│  │    - Track root span reference per subtrace                         │   │
│  │    - Apply aggregations when subtrace completes                     │   │
│  │                                                                      │   │
│  │  Aggregation Types:                                                  │   │
│  │    - first: First seen value                                        │   │
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
│  │    - Root span ends (primary trigger)                               │   │
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

### 2. Collector-Side: Stateful Subtrace Processor

**Location:** OpenTelemetry Collector processor

**Responsibilities:**
- Buffer spans by subtrace ID
- Track root span for each subtrace
- Apply configured aggregations
- Emit enriched spans when subtrace completes

**Configuration Schema:**
```yaml
processors:
  subtrace:
    # Timeout for incomplete subtraces
    timeout: 30s
    
    # Maximum spans to buffer per subtrace
    max_spans_per_subtrace: 1000
    
    # Aggregation rules
    aggregations:
      # Copy exception events to root span
      - source_event: "exception"
        target_event: "exception"
        copy_attributes:
          - "exception.type"
        add_attributes:
          exception.source_span_id: "${source_span_id}"
      
      # Count database calls
      - source_attribute: "db.system"
        target_attribute: "subtrace.db_call_count"
        aggregation: count
      
      # First seen loyalty status
      - source_attribute: "customer.loyalty_status"
        target_attribute: "subtrace.customer.loyalty_status"
        aggregation: first
        type: string
      
      # Sum of items processed
      - source_attribute: "items.count"
        target_attribute: "subtrace.total_items"
        aggregation: sum
        type: int
```

**Aggregation Types:**

| Aggregation | Input Type | Output Type | Description |
|-------------|------------|-------------|-------------|
| `first` | any | same | First non-null value seen |
| `last` | any | same | Last non-null value seen |
| `all` | any | array | All values (preserves duplicates) |
| `all_distinct` | any | array | All unique values |
| `count` | any | int | Count of non-null occurrences |
| `sum` | numeric | same | Sum of values |
| `avg` | numeric | double | Average of values |
| `min` | numeric | same | Minimum value |
| `max` | numeric | same | Maximum value |
| `copy_event` | event | event | Copy span events to root |

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
- source_event: "exception"
  target_event: "exception"
  copy_attributes:
    - "exception.type"
  add_attributes:
    exception.source_span_id: "${source_span_id}"
```

**Result on Root Span:**
```
Events:
  - name: "exception"
    attributes:
      exception.type: "PaymentFailedException"
      exception.source_span_id: "abc123def456"
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

**Collector Aggregation:**
```yaml
- source_attribute: "db.system"
  target_attribute: "subtrace.db_call_count"
  aggregation: count
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
- source_attribute: "customer.loyalty_status"
  target_attribute: "subtrace.customer.loyalty_status"
  aggregation: first
  type: string
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
3. **Max Subtraces:** Limit total subtraces in memory
4. **LRU Eviction:** Evict oldest subtraces when limits reached

```yaml
processors:
  subtrace:
    timeout: 30s
    max_spans_per_subtrace: 1000
    max_subtraces: 10000
    eviction_policy: lru
```

## Span Ordering Considerations

Spans may arrive out of order. The processor handles this by:

1. Buffering all spans until root span ends
2. Using span timestamps for ordering when needed
3. Applying aggregations based on logical order (parent before child)

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Root span never arrives | Flush on timeout, skip root-only aggregations |
| Duplicate subtrace ID | Merge spans (unlikely with proper hashing) |
| Invalid aggregation config | Log warning, skip aggregation |
| Memory pressure | Evict oldest subtraces, emit warning metric |

## Metrics Emitted by Processor

| Metric | Type | Description |
|--------|------|-------------|
| `subtrace_processor_active_subtraces` | Gauge | Current subtraces in memory |
| `subtrace_processor_spans_buffered` | Gauge | Total spans buffered |
| `subtrace_processor_subtraces_completed` | Counter | Subtraces successfully processed |
| `subtrace_processor_subtraces_timeout` | Counter | Subtraces flushed due to timeout |
| `subtrace_processor_subtraces_evicted` | Counter | Subtraces evicted due to memory |

## Next Steps

1. **Implement SubtraceIdProcessor** in Python for application services
2. **Expand demo services** with the three use cases
3. **Implement collector processor** (Go) or use transform processor as approximation
4. **Test end-to-end** with the demo application
