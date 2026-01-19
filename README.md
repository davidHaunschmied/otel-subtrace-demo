# OpenTelemetry Demo Application

A sample application demonstrating OpenTelemetry instrumentation with two microservices that communicate with each other. Perfect for testing OpenTelemetry collectors and processors.

## Architecture

```
┌─────────────┐     HTTP     ┌─────────────┐
│  Service A  │ ──────────► │  Service B  │
│  (API)      │             │  (Data)     │
└─────────────┘             └─────────────┘
       │                           │
       └─────────── OTLP ───────────┘
                    │
              ┌─────────────┐
              │   Collector │
              └─────────────┘
                    │
        ┌───────────┼───────────┐
        │           │           │
   ┌─────────┐ ┌─────────┐ ┌─────────┐
   │ Jaeger  │ │Dynatrace│ │Prometheus│
   │   UI    │ │   UI    │ │   UI    │
   └─────────┘ └─────────┘ └─────────┘
```

## Services

### Service A (Port 8001)
- **Role**: API Gateway/Frontend service
- **Functionality**: Receives HTTP requests and calls Service B for data processing
- **Endpoints**:
  - `GET /` - Health check
  - `GET /api/process/{data_id}` - Process data by calling Service B
  - `GET /api/health` - Health check with dependency status

### Service B (Port 8002)
- **Role**: Data processing service
- **Functionality**: Processes data and manages a mock database
- **Endpoints**:
  - `GET /` - Health check
  - `GET /api/data/{data_id}` - Retrieve and process data
  - `POST /api/data/{data_id}` - Create new data
  - `GET /api/data` - List all data IDs
  - `GET /api/health` - Detailed health check

## OpenTelemetry Features Demonstrated

### Tracing
- **Distributed tracing** across service boundaries
- **Baggage propagation** for context sharing
- **Span attributes** for metadata
- **Custom spans** for business logic
- **Error handling** and span status

### Metrics
- **Counters** for request tracking
- **Histograms** for processing time
- **Custom metrics** for business operations
- **Resource attributes** for service identification

### Collector Processors
- **Batch processor** for performance optimization
- **Attributes processor** for adding custom attributes
- **Transform processor** for data modification
- **Filter processor** for data filtering
- **Span metrics processor** for converting spans to metrics
- **Resource processor** for resource-level attributes

## Quick Start

### Prerequisites
- Docker and Docker Compose
- Git

### Running the Demo

1. **Clone and navigate to the project**:
   ```bash
   cd /path/to/otel-demo-project
   ```

2. **Start all services**:
   ```bash
   docker-compose up -d
   ```

3. **Wait for services to start** (30-60 seconds):
   ```bash
   docker-compose ps
   ```

4. **Test the services**:
   ```bash
   # Test Service A
   curl http://localhost:8001/

   # Test Service B
   curl http://localhost:8002/

   # Test service communication
   curl http://localhost:8001/api/process/user123
   ```

### Accessing Telemetry Data

- **Jaeger UI**: http://localhost:16686
- **Zipkin UI**: http://localhost:9411
- **Prometheus**: http://localhost:9090
- **Collector Health**: http://localhost:13133
- **Collector zPages**: http://localhost:55679/debug/pprof/

## Testing Scenarios

### 1. Basic Request Flow
```bash
curl http://localhost:8001/api/process/user123
```
This will:
- Create a trace in Service A
- Propagate context to Service B
- Create spans for database queries and data processing
- Generate metrics for both services

### 2. Error Handling
```bash
curl http://localhost:8001/api/process/nonexistent
```
This will demonstrate:
- Error span attributes
- Error metrics
- Proper trace completion with error status

### 3. Create New Data
```bash
curl -X POST http://localhost:8002/api/data/user999 \
  -H "Content-Type: application/json" \
  -d '{"name": "Test User", "email": "test@example.com", "age": 28}'
```

### 4. Generate Load
```bash
# Generate multiple requests to see traces and metrics
for i in {1..10}; do
  curl http://localhost:8001/api/process/user123 &
done
wait
```

## OpenTelemetry Collector Configuration

The collector configuration (`otel-collector-config.yaml`) includes:

### Receivers
- **OTLP** (gRPC & HTTP) - Receives traces and metrics from services
- **Prometheus** - Scrapes metrics

### Processors
- **batch** - Batches data for better performance
- **memory_limiter** - Prevents OOM
- **attributes** - Adds custom attributes
- **transform** - Modifies span and metric data
- **spanmetrics** - Converts spans to metrics
- **filter** - Filters data based on patterns

### Exporters
- **debug** - Outputs to console (useful for development)
- **prometheus** - Exposes metrics for scraping
- **jaeger** - Sends traces to Jaeger
- **zipkin** - Sends traces to Zipkin
- **logging** - Logs data

## Development

### Running Services Locally

1. **Install dependencies**:
   ```bash
   pip install -r requirements.txt
   ```

2. **Start the collector** (in Docker):
   ```bash
   docker-compose up otel-collector jaeger zipkin prometheus -d
   ```

3. **Run services locally**:
   ```bash
   # Terminal 1
   python service_a.py

   # Terminal 2
   python service_b.py
   ```

### Customizing the Collector

Edit `otel-collector-config.yaml` to:
- Add/remove processors
- Change export destinations
- Modify filtering rules
- Adjust batch sizes and timeouts

### Adding Custom Metrics

```python
# In your service code
counter = meter.create_counter("custom_operations_total")
counter.add(1, {"operation": "custom", "status": "success"})

histogram = meter.create_histogram("custom_duration_seconds")
histogram.record(duration, {"operation": "custom"})
```

### Adding Custom Spans

```python
# In your service code
with tracer.start_as_current_span("custom-operation") as span:
    span.set_attribute("custom.attribute", "value")
    # Your custom logic here
```

## Monitoring and Debugging

### Collector Health
```bash
curl http://localhost:13133
```

### Collector Metrics
```bash
curl http://localhost:8888/metrics
```

### Service Logs
```bash
docker-compose logs service-a
docker-compose logs service-b
docker-compose logs otel-collector
```

## Troubleshooting

### Common Issues

1. **Services not starting**: Check Docker logs for errors
2. **No traces in Jaeger**: Verify collector is running and services are configured correctly
3. **No metrics in Prometheus**: Check Prometheus configuration and collector metrics endpoint
4. **Service communication errors**: Ensure both services are running and accessible

### Debug Commands
```bash
# Check all services
docker-compose ps

# View logs
docker-compose logs -f

# Test connectivity
curl http://localhost:8001/api/health
curl http://localhost:8002/api/health

# Check collector configuration
docker-compose exec otel-collector otelcol --config=/etc/otel-collector-config.yaml validate
```

## Clean Up

```bash
docker-compose down -v
```

This will stop and remove all containers, networks, and volumes.

## Next Steps

This demo is designed to be a foundation for testing OpenTelemetry collectors and processors. You can:

1. **Modify the collector configuration** to test different processors
2. **Add more services** to create complex distributed systems
3. **Implement custom processors** in the collector
4. **Add different telemetry backends** (e.g., Tempo, Loki)
5. **Experiment with different sampling strategies**
6. **Add log correlation** with OpenTelemetry logging
