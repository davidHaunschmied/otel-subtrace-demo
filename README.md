# Subtrace: Service-Level Real-Time Analytics for OpenTelemetry

Demonstrate powerful **service-level real-time analytics** using the **Subtrace pattern** — a technique that groups all spans within a service for a given trace, enabling deep insights that traditional distributed tracing cannot provide.

## What is a Subtrace?

A **subtrace** is a logical grouping of all spans created within a single service during the processing of one trace. While a distributed trace spans multiple services, a subtrace focuses on what happens *inside* one service.

```
Distributed Trace (spans across services):
┌─────────────────────────────────────────────────────────────────────┐
│ Service A          │ Service B          │ Service C                │
│ ┌────────────────┐ │ ┌────────────────┐ │ ┌────────────────┐       │
│ │ Subtrace A     │ │ │ Subtrace B     │ │ │ Subtrace C     │       │
│ │  ├─ span 1     │─┼─│  ├─ span 1     │─┼─│  ├─ span 1     │       │
│ │  ├─ span 2     │ │ │  ├─ span 2     │ │ │  └─ span 2     │       │
│ │  └─ span 3     │ │ │  └─ span 3     │ │ └────────────────┘       │
│ └────────────────┘ │ └────────────────┘ │                          │
└─────────────────────────────────────────────────────────────────────┘
```

Each subtrace has:
- **`subtrace.id`**: Unique identifier (hash of trace_id + resource attributes)
- **`subtrace.is_root_span`**: Marks the topmost span in the subtrace

## Why Subtraces?

Traditional tracing answers: *"What services did this trace touch?"*

Subtraces answer: *"What happened inside each service?"*

### Real-Time Analytics Enabled by Subtraces

| Analytics | Description | Example |
|-----------|-------------|---------|
| **N+1 Query Detection** | Count DB calls per request | `subtrace.db_call_count: 47` → N+1 detected! |
| **Business Exception Tracking** | Surface caught exceptions | PaymentFailedException in child span → visible on root |
| **Customer Segmentation** | Propagate business context | `subtrace.customer.loyalty_status: "gold"` |
| **Resource Aggregation** | Sum/avg across child spans | `subtrace.total_bytes_processed: 1.2MB` |

## Architecture

```
┌─────────────┐     HTTP     ┌─────────────┐
│  Service A  │ ──────────► │  Service B  │
│  (API)      │             │  (Data)     │
└──────┬──────┘             └──────┬──────┘
       │                           │
       │           OTLP            │
       └─────────────┬─────────────┘
                     │
                     ▼
       ┌─────────────────────────────┐
       │         Collector           │
       │    subtraceaggregator:      │
       │    - groups by trace+resource
       │    - calculates subtrace.id │
       │    - detects root span      │
       │    - aggregates attributes  │
       └─────────────┬───────────────┘
                     │
           ┌─────────┴─────────┐
           │                   │
           ▼                   ▼
     ┌──────────┐        ┌──────────┐
     │  Jaeger  │        │Dynatrace │
     │  (dev)   │        │  (prod)  │
     └──────────┘        └──────────┘
```

## How It Works

### Subtrace Aggregator Processor

The `subtraceaggregator` processor runs in the OpenTelemetry Collector. It:

1. **Buffers spans by trace ID** — Groups all spans from the same trace
2. **Groups by resource attributes** — Spans with the same resource attributes (same service) form a subtrace
3. **Calculates `subtrace.id`** — Hash of (trace_id + resource_attributes) ensures deterministic IDs
4. **Detects root span** — Finds the topmost span in each subtrace (no parent in the subtrace)
5. **Aggregates attributes** — Copies/aggregates data from child spans to the root span
6. **Sets attributes** — Adds `subtrace.id` and `subtrace.is_root_span` to all spans

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
      
      # Capture customer tier (first encountered value)
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

## Quick Start

### Prerequisites
- Docker and Docker Compose
- Python 3.9+

### Run the Demo

```bash
# Start infrastructure
docker-compose up -d

# Generate traces with subtrace IDs
curl http://localhost:8001/api/process/user123
```

### View Results

- **Jaeger UI**: http://localhost:16686 — See `subtrace.id` and `subtrace.is_root_span` attributes
- **Dynatrace**: Configure `DT_ENDPOINT` and `DT_API_TOKEN` in `.env`

## Demo Services

### Service A (Port 8001) — API Gateway
- Receives requests, calls Service B
- Demonstrates subtrace root span creation
- Endpoints: `GET /`, `GET /api/process/{id}`, `GET /api/health`

### Service B (Port 8002) — Data Service
- Processes data with multiple child spans
- Simulates database queries (for N+1 detection demo)
- Endpoints: `GET /`, `GET /api/data/{id}`, `POST /api/data/{id}`

## Key Use Cases

### 1. N+1 Query Detection

```bash
curl http://localhost:8001/api/process/user123
```

In Jaeger, find the root span and check:
- `subtrace.db_call_count` — High numbers indicate N+1 problems
- All spans share the same `subtrace.id`

### 2. Business Exception Visibility

When a `PaymentFailedException` is caught and handled gracefully (returning 200 OK), traditional monitoring misses it. With subtraces:
- Exception event recorded on child span
- Aggregated to root span for alerting
- Query: *"Show all requests with caught PaymentFailedException"*

### 3. Customer-Centric Analytics

```python
# Deep in your code
span.set_attribute("customer.loyalty_status", "gold")
```

With subtrace aggregation:
- Value propagates to root span as `subtrace.customer.loyalty_status`
- Build dashboards: *"P99 latency by customer tier"*

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Collector endpoint | `http://localhost:4317` |
| `DT_ENDPOINT` | Dynatrace OTLP endpoint | — |
| `DT_API_TOKEN` | Dynatrace API token | — |

### Collector Config

See `otel-collector-config.yaml` for the full configuration. Key exporters:
- **Jaeger**: Local development visualization
- **Dynatrace**: Production observability platform
- **Prometheus**: Metrics collection

## Development

### Run Services Locally

```bash
pip install -r requirements.txt

# Terminal 1: Start infrastructure
docker-compose up otel-collector jaeger prometheus -d

# Terminal 2: Run Service B
python service_b.py

# Terminal 3: Run Service A
python service_a.py
```

### Test the Subtrace Pattern

```bash
# Single request
curl http://localhost:8001/api/process/user123

# Load test
python test_services.py
```

## Documentation

- **[Subtrace Aggregator Spec](docs/SUBTRACEAGGREGATOR_PROCESSOR_SPEC.md)** — Complete processor specification with OTTL syntax
- **[Architecture Deep Dive](docs/ARCHITECTURE_STATEFUL_SUBTRACE_PROCESSOR.md)** — System design and data flow

## Roadmap

- [x] Demo services with subtrace support
- [x] Subtrace Aggregator Processor Spec
- [x] Subtrace Aggregator Processor (Go implementation)
- [ ] Dynatrace dashboard templates
- [ ] N+1 query alerting rules

## Building the Custom Collector

The custom collector with `subtraceaggregator` is built automatically via docker-compose:

```bash
docker-compose build otel-collector
```

Or build manually:

```bash
docker build -f collector/Dockerfile -t otelcol-subtrace .
```

See `processor/subtraceaggregator/` for the implementation and `collector/builder-config.yaml` for the build configuration.

## Clean Up

```bash
docker-compose down -v
```
