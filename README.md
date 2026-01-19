# Subtrace: Service-Level Real-Time Analytics for OpenTelemetry

Demonstrate powerful **service-level real-time analytics** using the **Subtrace pattern** — a technique that groups all spans within a service for a given trace, enabling deep insights that traditional distributed tracing cannot provide.

## What is a Subtrace?

A **subtrace** is a logical grouping of all spans created within a single service during the processing of one request. While a distributed trace spans multiple services, a subtrace focuses on what happens *inside* one service.

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
- **`subtrace.id`**: Unique identifier (hash of trace_id + first span_id in service)
- **`subtrace.is_root_span`**: Marks the service entry point span

## Why Subtraces?

Traditional tracing answers: *"What services did this request touch?"*

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
└─────────────┘             └─────────────┘
       │                           │
       │    SubtraceIdProcessor    │
       │    assigns subtrace.id    │
       │                           │
       └─────────── OTLP ───────────┘
                    │
              ┌─────────────┐
              │  Collector  │
              │  (enriches  │
              │  root spans)│
              └─────────────┘
                    │
        ┌───────────┴───────────┐
        │                       │
   ┌─────────┐            ┌──────────┐
   │ Jaeger  │            │Dynatrace │
   │  (dev)  │            │ (prod)   │
   └─────────┘            └──────────┘
```

## How It Works

### 1. SubtraceIdProcessor (Application-Side)

Each service runs a `SubtraceIdProcessor` that:
- Generates a unique `subtrace.id` for the first span in the service
- Marks that span as `subtrace.is_root_span = true`
- Propagates `subtrace.id` to all child spans

```python
from subtrace_processor import SubtraceIdProcessor

# Add to your TracerProvider
trace_provider.add_span_processor(SubtraceIdProcessor())
```

### 2. Collector Aggregation (Future)

A stateful collector processor can aggregate child span data onto the root span:

```yaml
processors:
  subtrace:
    aggregations:
      # Count database calls (N+1 detection)
      - source_attribute: "db.system"
        target_attribute: "subtrace.db_call_count"
        aggregation: count
      
      # Propagate exceptions to root span
      - source_event: "exception"
        target_event: "exception"
        aggregation: copy_event
      
      # Capture customer tier
      - source_attribute: "customer.loyalty_status"
        target_attribute: "subtrace.customer.loyalty_status"
        aggregation: first
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

- **[Architecture Deep Dive](docs/ARCHITECTURE_STATEFUL_SUBTRACE_PROCESSOR.md)** — Detailed design of the subtrace processor
- **[Collector Configuration](docs/COLLECTOR_SUBTRACE_PROCESSOR_CONFIG.md)** — Future stateful processor config schema

## Roadmap

- [x] SubtraceIdProcessor (Python SDK)
- [x] Demo services with subtrace support
- [ ] Stateful Collector Processor (Go)
- [ ] Dynatrace dashboard templates
- [ ] N+1 query alerting rules

## Clean Up

```bash
docker-compose down -v
```
