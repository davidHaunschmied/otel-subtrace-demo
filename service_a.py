"""Service A - API Gateway"""

import logging
import os

from fastapi import FastAPI, HTTPException
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.instrumentation.requests import RequestsInstrumentor
from opentelemetry.sdk.resources import Resource, SERVICE_NAME
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
import requests

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


def setup_opentelemetry():
    resource = Resource(attributes={SERVICE_NAME: "service-a"})
    trace_provider = TracerProvider(resource=resource)
    otel_endpoint = os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
    trace_provider.add_span_processor(BatchSpanProcessor(
        OTLPSpanExporter(endpoint=otel_endpoint, insecure=True)
    ))
    trace.set_tracer_provider(trace_provider)
    return trace_provider


trace_provider = setup_opentelemetry()
tracer = trace.get_tracer(__name__)
RequestsInstrumentor().instrument()

app = FastAPI(title="Service A")
FastAPIInstrumentor.instrument_app(app, tracer_provider=trace_provider)


@app.get("/")
async def root():
    return {"service": "service-a", "status": "healthy"}


@app.get("/api/process/{customer_id}")
async def process_customer(customer_id: str):
    """Call Service B and return result."""
    service_b_url = os.getenv("SERVICE_B_URL", "http://localhost:8002")
    
    with tracer.start_as_current_span("process-customer") as span:
        span.set_attribute("customer.id", customer_id)
        
        if not customer_id.startswith("user"):
            raise HTTPException(status_code=400, detail="Invalid customer ID format")
        
        try:
            response = requests.get(f"{service_b_url}/api/data/{customer_id}", timeout=10)
            if response.status_code == 404:
                raise HTTPException(status_code=404, detail="Customer not found")
            response.raise_for_status()
            return {"service": "service-a", "result": response.json()}
        except requests.exceptions.RequestException as e:
            logger.error(f"Service B error: {e}")
            raise HTTPException(status_code=503, detail="Service B unavailable")


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8001)
