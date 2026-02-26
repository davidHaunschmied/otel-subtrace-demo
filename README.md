# Subtrace Aggregator Demo

A **subtrace** groups all spans within a single service for a given trace, enabling analytics like N+1 query detection and exception propagation to root spans.

## Quick Start

```bash
docker-compose up -d
curl http://localhost:18001/api/process/user123
```

**Jaeger UI**: http://localhost:26686 — Look for `subtrace.id` and `subtrace.db_call_count` attributes.

## How It Works

```
Service A ──HTTP──► Service B
    │                   │
    └───────OTLP────────┘
            │
            ▼
    ┌───────────────────┐
    │ OTel Collector    │
    │ subtraceaggregator│
    └─────────┬─────────┘
              │
        ┌─────┴─────┐
        ▼           ▼
     Jaeger    Dynatrace
```

The `subtraceaggregator` processor:
1. Buffers spans by trace ID
2. Groups by resource attributes (same service = same subtrace)
3. Sets `subtrace.id` on all spans
4. Aggregates child span data onto the root span

## Processor Configuration

```yaml
processors:
  subtraceaggregator:
    timeout: 30s
    max_spans_per_subtrace: 500
    
    attribute_aggregations:
      - aggregation: count
        condition: 'attributes["db.system"] != nil'
        target: subtrace.db_call_count
      
      - aggregation: any
        source: attributes["customer.loyalty_status"]
        target: subtrace.customer.loyalty_status
    
    event_aggregations:
      - aggregation: copy_event
        source: exception
        max_events: 5
```

**Aggregation types**: `count`, `sum`, `avg`, `min`, `max`, `any`, `all`, `all_distinct`, `copy_event`

## Demo Use Cases

| Use Case | How to See It |
|----------|---------------|
| **N+1 Detection** | Check `subtrace.db_call_count` on root span |
| **Exception Propagation** | Payment failures (~30% chance) copied to root span |
| **Attribute Propagation** | `customer.loyalty_status` from child → root |

## Development

```bash
pip install -r requirements.txt
docker-compose up otel-collector jaeger -d
python service_b.py  # Terminal 1
python service_a.py  # Terminal 2
```

## Building the Collector

```bash
docker-compose build otel-collector
```

See `processor/subtraceaggregator/` for the Go implementation.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `DT_ENDPOINT` | Dynatrace OTLP endpoint |
| `DT_API_TOKEN` | Dynatrace API token |

## Cleanup

```bash
docker-compose down -v
```
