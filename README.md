# Subtrace Aggregation with OpenTelemetry

> **Unlock service-level insights from your distributed traces — with stock Collector components**

In distributed tracing, a single trace can span multiple services—but what if you want to analyze behavior *within* a single service? That's where **subtraces** come in.

A **subtrace** groups the spans of one call to one service within a trace, enabling analytics that traditional tracing can't provide:

- **🔍 N+1 Query Detection** — Count database and downstream calls per request and spot inefficient patterns
- **⚠️ Error Propagation** — Carry the error status message of any inner span to the service entry point
- **📊 Business Context Enrichment** — Copy important attributes (like customer loyalty status) to the entry span for easier querying

No custom Collector build is needed. Two components of the
[contrib distribution](https://github.com/open-telemetry/opentelemetry-collector-contrib) (v0.163.0 or later) do it:

1. [`groupbytrace`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/processor/groupbytraceprocessor) with `emit_strategy: service` buffers spans and releases **one batch per call to a service**.
2. [`transform`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/processor/transformprocessor) with `shared_cache: true` aggregates over that batch: a first statement group collects values from every span, a second writes them to the span that entered the call.

> An earlier version of this repo shipped a custom `subtraceaggregator` processor. It is superseded by the configuration below; see the git history if you need it.

## Quick Start

```bash
export DT_OTLP_ENDPOINT=https://<environment-id>.live.dynatrace.com/api/v2/otlp
export DT_INGEST_TOKEN=<platform token with OTLP ingest permissions>
docker compose up -d --build
curl http://localhost:18001/api/process/user123
```

Open **Jaeger UI** at http://localhost:26686 and look at the `GET /api/data/{customer_id}` span of `service-b`:
- `subtrace.id` on every span (the ID of the span that entered the call)
- `subtrace.is_root_span` on the entry span
- `subtrace.db_call_count` and `subtrace.tables_accessed` (aggregated from child spans)
- `subtrace.error_message` when the payment failed
- `subtrace.customer.loyalty_status` copied from a child span

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
       ┌─────────────────────────────┐
       │ OTel Collector (contrib)    │
       │  groupbytrace               │
       │   emit_strategy: service    │
       │  transform/summarize_subtrace│
       │   shared_cache: true        │
       └───────────┬─────────────────┘
                   │
           ┌───────┴───────┐
           ▼               ▼
        Jaeger        Dynatrace
```

### Subtrace boundaries

`groupbytrace` decides which spans form one call to a service. A span starts a new call when

1. its parent span ID is empty, or
2. the `IS_REMOTE` flag in `Span.flags` is set (a service entry span), or
3. its parent belongs to a different service (`service.namespace`, `service.name`, `service.instance.id`).

A service entered several times in one trace (`A → B → C → B`) yields one batch per call. Spans whose parent never arrives are released together as one best-effort batch.

### The aggregation recipe

[`otel-collector-config.yaml`](otel-collector-config.yaml) holds the full configuration. The pattern:

```yaml
transform/summarize_subtrace:
  trace_statements:
    - context: span                       # pass 1: collect over all spans
      shared_cache: true
      statements:
        - set(cache["span_ids"][span.span_id.string], true)
        - set(cache["db_calls"], 0) where cache["db_calls"] == nil
        - set(cache["db_calls"], cache["db_calls"] + 1) where span.attributes["db.system"] != nil
    - context: span                       # pass 2: write onto the entry span
      shared_cache: true
      conditions:
        - cache["span_ids"][span.parent_span_id.string] == nil
      statements:
        - set(span.attributes["subtrace.db_call_count"], cache["db_calls"])
```

Aggregations from the original processor map to OTTL as follows:

| Aggregation | OTTL pattern |
|---|---|
| `count` | counter in the cache, `+ 1` per matching span |
| `sum` / `avg` | running sum (and count) in the cache |
| `min` / `max` | `set(cache["max"], x) where cache["max"] == nil or x > cache["max"]` |
| `any` | `set(cache["v"], x) where cache["v"] == nil` |
| `all` / `all_distinct` | `append(...)`, with `not ContainsValue(...)` for distinct |
| per-key counts | dynamic keys: `cache["outbound"][span.name]` |

Limits:

- `transform` must directly follow `groupbytrace`; a processor in between that merges or splits batches (e.g. `batch`) breaks the subtrace scope.
- Copying span **events** (e.g. exceptions) to the entry span isn't possible in OTTL. Use the span's error status message instead, as this demo does.

## Demo Use Cases

### 🔍 N+1 Query Detection
Service B makes 5 database queries per request. The entry span gets `subtrace.db_call_count = 5` and the distinct tables in `subtrace.tables_accessed`.

### ⚠️ Error Propagation
Payment processing fails ~30% of the time and sets an error status on the `process-payment` span. Its message (e.g. `Payment failed: card_declined`) is copied to `subtrace.error_message` on the entry span, together with `subtrace.error_count`.

### 📊 Business Context Enrichment
The `customer.loyalty_status` attribute (gold/platinum/silver) is set deep in a child span and copied to `subtrace.customer.loyalty_status` on the entry span, enabling queries like "show me all requests from platinum customers."

## Astroshop (OpenTelemetry Demo) with Dynatrace

[`astroshop/`](astroshop/) applies the same approach to the [OpenTelemetry Demo](https://github.com/open-telemetry/opentelemetry-demo) and sends traces, metrics and logs to Dynatrace. Every call to a service gets these attributes on its entry span:

| Attribute | Feature |
|---|---|
| `subtrace.db_call_count` | N+1: database calls during the call |
| `subtrace.outbound_calls` | N+1: outgoing calls per operation, e.g. one `GetProduct` per cart item in checkout |
| `subtrace.error_count`, `subtrace.error_message` | error status message of the first failing inner span (`paymentFailure` flag at 25%) |
| `subtrace.user.loyalty_level` | business context set on payment's inner `charge` span |

All data carries the resource attribute `deployment.release_product = "astroshop-subtrace-demo"`, a primary Grail field, for filtering.

| File | Purpose |
|---|---|
| `otelcol-config-extras.yml` | showcase processors, Dynatrace exporter, `deployment.release_product`, delta temporality |
| `compose.extras.yaml` | memory limits for the `latest` images, browser load disabled (Playwright memory leak), Dynatrace env |
| `compose.remote.yaml` | puts every container into `astroshop.slice`; health checks every 60s after startup instead of 5s (their dockerd/containerd cost is outside the slice); 2 load-generator users |
| `astroshop.slice` | systemd resource budget for a shared host: 1 CPU on cores 2–3, 3 GiB, minimum CPU and IO weight, so co-located workloads win under contention |
| `astroshop.sh` | `up` / `down` / `ps` / `logs` wrapper (core services only, no Jaeger/Prometheus/Grafana/OpenSearch; always pulls, since the demo's `latest` tags move) |
| `install.sh` | checks out the tested demo commit and installs everything above |
| `openpipeline-span-metrics.json` | OpenPipeline metric extraction processors for the span pipeline: `astroshop.subtrace.calls`, `.failed_calls`, `.db_calls`, `.errors`, `.duration`, each by `service.name` and `endpoint.name` |
| `failure-detection-ruleset.json` | failure detection ruleset (`builtin:failure-detection-rulesets`): the defaults plus "a call fails if an inner span failed" (`subtrace.error_count > 0`), scoped to `deployment.release_product` |
| `dynatrace-notebook.yaml` | story notebook "The case of the failing checkout": symptom → root cause → affected customers → N+1, with the endpoint metrics as line charts: `dtctl create notebook -f astroshop/dynatrace-notebook.yaml` |

```bash
astroshop/install.sh ~/opentelemetry-demo     # on the target host (systemd, Docker)
vi ~/opentelemetry-demo/.env.dynatrace        # DT_OTLP_ENDPOINT, DT_INGEST_TOKEN
~/opentelemetry-demo/astroshop.sh up
```

The shop is then served on port 8090 (`/feature/` toggles feature flags).

Tenant configuration with [dtctl](https://github.com/dynatrace-oss/dtctl):

```bash
# Metric extraction: add the processors to the span pipeline that receives the demo's spans
# (here "All spans"; check builtin:openpipeline.spans.routing).
dtctl get settings --schema builtin:openpipeline.spans.pipelines -o json \
  | jq --slurpfile p astroshop/openpipeline-span-metrics.json \
      '.result[] | select(.value.displayName == "All spans")
       | {objectId, schemaId, scope, value: (.value + {metricExtraction: {processors: $p[0]}})}' > pipeline.json
dtctl apply -f pipeline.json

# Failure detection: rulesets are ordered and the first match wins. A new object is
# appended after "[Built-in] Default failure detection" and would never match, so pass
# insertAfter = objectId of the ruleset listed directly above the default.
dtctl get settings --schema builtin:failure-detection-rulesets -o json \
  | jq -r '.result[] | [.objectId, .value.ruleset.rulesetName] | @tsv'
jq --arg after "<objectId>" \
  '{schemaId: "builtin:failure-detection-rulesets", scope: "environment", insertAfter: $after, value: .}' \
  astroshop/failure-detection-ruleset.json > ruleset.json
dtctl apply -f ruleset.json
```

If the default is the only ruleset, create it without `insertAfter` and move it above the default in the Settings UI.

The `metricExtraction` stage replaces any existing extraction processors of that pipeline. Merge them if it has some.

## Development

```bash
pip install -r requirements.txt
docker compose up otel-collector jaeger -d
python service_b.py  # Terminal 1
python service_a.py  # Terminal 2
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `DT_OTLP_ENDPOINT` | Dynatrace OTLP endpoint, `https://<environment-id>.live.dynatrace.com/api/v2/otlp` |
| `DT_INGEST_TOKEN` | Dynatrace platform token with OTLP ingest permissions (sent as `Authorization: Bearer`) |

## Cleanup

```bash
docker compose down -v
```
