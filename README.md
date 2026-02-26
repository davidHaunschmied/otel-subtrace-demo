# Subtrace Aggregator for OpenTelemetry

> **Unlock service-level insights from your distributed traces**

In distributed tracing, a single trace can span multiple services—but what if you want to analyze behavior *within* a single service? That's where **subtraces** come in.

A **subtrace** groups all spans belonging to the same service within a trace, enabling powerful analytics that traditional tracing can't provide:

- **🔍 N+1 Query Detection** — Count database calls per request and spot inefficient patterns
- **⚠️ Exception Bubbling** — Propagate exceptions from any child span to the service entry point
- **📊 Business Context Enrichment** — Copy important attributes (like customer tier) to root spans for easier querying

This project demonstrates a custom OpenTelemetry Collector processor that implements subtrace aggregation.

## Quick Start

```bash
docker compose up -d
curl http://localhost:18001/api/process/user123
```

Open **Jaeger UI** at http://localhost:26686 and look for:
- `subtrace.id` on all spans (groups spans by service)
- `subtrace.db_call_count` on root spans (aggregated from children)
- `subtrace.customer.loyalty_status` propagated to root span

## Architecture

```
┌─────────────┐         ┌─────────────┐
│  Service A  │──HTTP──►│  Service B  │
│ (Gateway)   │         │ (Data Svc)  │
└──────┬──────┘         └──────┬──────┘
       │                       │
       └───────OTLP────────────┘
                   │
                   ▼
       ┌───────────────────────┐
       │   OTel Collector      │
       │  ┌─────────────────┐  │
       │  │ subtraceaggregator │
       │  │   processor     │  │
       │  └─────────────────┘  │
       └───────────┬───────────┘
                   │
           ┌───────┴───────┐
           ▼               ▼
        Jaeger        Dynatrace
```

**How the processor works:**
1. Buffers incoming spans by trace ID
2. Groups spans by resource attributes (same service = same subtrace)
3. Assigns a unique `subtrace.id` to each group
4. Identifies the root span of each subtrace
5. Aggregates data from child spans onto the root span
6. Flushes after timeout or max spans reached

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

### 🔍 N+1 Query Detection
Service B makes 5 database queries per request. The processor counts spans with `db.system` attribute and sets `subtrace.db_call_count = 5` on the root span. Query this in your observability platform to find services with excessive DB calls.

### ⚠️ Exception Propagation  
Payment processing fails ~30% of the time. When it does, the `exception` event is copied from the child span to the subtrace root span, making failures visible at the service entry point without drilling down.

### 📊 Business Context Enrichment
The `customer.loyalty_status` attribute (gold/platinum/silver) is set deep in a child span. The processor propagates it to the root span using the `any` aggregation, enabling queries like "show me all requests from platinum customers."

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
