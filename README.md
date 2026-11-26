# The power of service transaction analytics, shown on the OpenTelemetry Astroshop

A **service transaction** is one call to one service within a trace: the span that entered the service plus everything it did before returning. Summarizing each service transaction onto its entry span answers questions that per-span analytics can't:

- **🔍 N+1 detection:** how many database and downstream calls did this call make?
- **⚠️ Error propagation:** which inner span failed, and with what status message?
- **📊 Business context:** which customer segment did this call serve?

No custom Collector component is needed. This repo is a configuration for the [OpenTelemetry Demo](https://github.com/open-telemetry/opentelemetry-demo) (astroshop) using two stock [contrib](https://github.com/open-telemetry/opentelemetry-collector-contrib) processors, plus a Dynatrace setup: one failure detection rule makes the built-in service metrics count inner failures per endpoint, and a notebook tells the story.

| File | What it adds |
|---|---|
| [`opentelemetry-demo/src/otel-collector/otelcol-config-extras.yml`](opentelemetry-demo/src/otel-collector/otelcol-config-extras.yml) | the service transaction recipe; Dynatrace export |
| [`opentelemetry-demo/compose.extras.yaml`](opentelemetry-demo/compose.extras.yaml) | Dynatrace credentials; memory limits and disabled browser load so the demo runs stably |
| [`dynatrace/failure-detection-ruleset.json`](dynatrace/failure-detection-ruleset.json) | a call fails when any span inside it failed, so built-in `dt.service.request.*` metrics are right per endpoint |
| [`dynatrace/notebook.yaml`](dynatrace/notebook.yaml) | story notebook *The power of service transaction analytics, shown on the OpenTelemetry Astroshop* |

## The recipe

1. [`groupbytrace`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/processor/groupbytraceprocessor) with `emit_strategy: service` (v0.163.0+) releases **one batch per service transaction**. A span starts a new transaction when its parent is empty, the `IS_REMOTE` span flag is set, or its parent belongs to another service.
2. [`transform`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/processor/transformprocessor) with `shared_cache: true` aggregates over that batch. A first statement group collects over every span, and a second writes onto the span whose parent isn't in the batch, which is the entry span.

```yaml
transform/summarize_transaction:
  trace_statements:
    - context: span                       # pass 1: collect over all spans of the call
      shared_cache: true
      statements:
        - set(cache["span_ids"][span.span_id.string], true)
        - set(cache["db_calls"], 0) where cache["db_calls"] == nil
        - set(cache["db_calls"], cache["db_calls"] + 1) where span.attributes["db.system.name"] != nil
    - context: span                       # pass 2: write onto the entry span
      shared_cache: true
      conditions:
        - cache["span_ids"][span.parent_span_id.string] == nil
      statements:
        - set(span.attributes["transaction.db_call_count"], cache["db_calls"])
```

Transaction attributes, set on every entry span:

| Attribute | Meaning |
|---|---|
| `transaction.is_root_span` | `true`: this span entered the transaction |
| `transaction.db_call_count` | database calls during the transaction |
| `transaction.outbound_calls` | outgoing calls per operation (map), e.g. one `GetProduct` per cart item |
| `transaction.error_count` | spans in the transaction with error status |
| `transaction.error_message` | status message of the first failing inner (non-server) span |
| `transaction.user.loyalty_level` | copied from payment's inner `charge` span |

Other aggregations in OTTL: `sum`/`avg` as running sums in the cache; `min`/`max` with `where cache["max"] == nil or x > cache["max"]`; distinct values with `append` and `not ContainsValue(...)`; per-key counts with dynamic keys (`cache["outbound"][span.name]`).

Limits:

- `transform` must directly follow `groupbytrace`. A processor in between that merges or splits batches (e.g. `batch`) breaks the transaction scope.
- OTTL can't copy span events, so use the error **status message**, not exceptions.
- A service transaction only sees its own spans. If a client span records an error without a message, there is nothing to lift.

## Run it

```bash
git clone https://github.com/davidHaunschmied/otel-subtrace-demo.git
git clone https://github.com/open-telemetry/opentelemetry-demo.git && cd opentelemetry-demo
git checkout 6c2a814b9c74b72d5d6a2fd22281dad4ba246512   # tested commit
cp -R ../otel-subtrace-demo/opentelemetry-demo/. .     # the two extras files

export DT_OTLP_ENDPOINT=https://<environment-id>.live.dynatrace.com/api/v2/otlp
export DT_INGEST_TOKEN=<platform token with OTLP ingest permissions for traces, metrics, logs>
make start-minimal-no-o11y                              # core services + extras, no Jaeger/Prometheus/Grafana
```

To get payment errors, open http://localhost:8080/feature and set the `paymentFailure` flag to `25%`.

All data carries `deployment.release_product = "astroshop-subtrace-demo"`, a primary Grail field, for filtering. The `cumulativetodelta` processor is there because Dynatrace ingests delta temporality only.

## Dynatrace setup

With [dtctl](https://github.com/dynatrace-oss/dtctl), from this repo:

```bash
# 1. Failure detection: rulesets are ordered, first match wins. A new one is appended after
#    "[Built-in] Default failure detection" and would never match, so insert it above:
#    insertAfter = objectId of the ruleset directly above the default (if the default is
#    first, create it and move it up in the Settings UI).
dtctl get settings --schema builtin:failure-detection-rulesets -o json \
  | jq -r '.result[] | [.objectId, .value.ruleset.rulesetName] | @tsv'
jq --arg after "<objectId>" \
  '{schemaId: "builtin:failure-detection-rulesets", scope: "environment", insertAfter: $after, value: .}' \
  dynatrace/failure-detection-ruleset.json > ruleset.json
dtctl apply -f ruleset.json

# 2. Notebook
dtctl create notebook -f dynatrace/notebook.yaml
```

No metric extraction is needed. The shop's edge answers failed orders with HTTP 422, which the default rules treat as success. With `transaction.error_count > 0`, the built-in `dt.service.request.failure_count` fails the edge endpoint too. Segment-level views (loyalty level, DB calls per transaction) query the entry spans directly.
