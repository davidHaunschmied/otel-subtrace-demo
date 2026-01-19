"""
Service A - API Gateway with Subtrace Support

Receives HTTP requests and calls Service B for data processing.
Uses SubtraceIdProcessor to assign subtrace IDs to all spans.
"""

import logging
import os
import random
import time

from fastapi import FastAPI, HTTPException
from opentelemetry import trace, metrics
from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.sdk.resources import SERVICE_NAME, SERVICE_VERSION, SERVICE_INSTANCE_ID

from subtrace_processor import SubtraceIdProcessor
from opentelemetry.instrumentation.requests import RequestsInstrumentor

import requests

# Configure logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Global variables for telemetry
tracer = None
meter = None
request_counter = None
processing_time_histogram = None


def setup_opentelemetry():
    """Configure OpenTelemetry tracing and metrics with SubtraceIdProcessor"""
    global tracer, meter, request_counter, processing_time_histogram

    resource = Resource(attributes={
        SERVICE_NAME: "service-a",
        SERVICE_VERSION: "2.0.0",
        SERVICE_INSTANCE_ID: "service-a-1"
    })

    # Configure tracing with SubtraceIdProcessor
    trace_provider = TracerProvider(resource=resource)
    trace_provider.add_span_processor(SubtraceIdProcessor())
    
    # Then add the exporter processor
    otel_endpoint = os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
    otlp_exporter = OTLPSpanExporter(endpoint=otel_endpoint, insecure=True)
    trace_provider.add_span_processor(BatchSpanProcessor(otlp_exporter))
    
    trace.set_tracer_provider(trace_provider)

    # Configure metrics
    metric_reader = PeriodicExportingMetricReader(
        exporter=OTLPMetricExporter(endpoint=otel_endpoint, insecure=True),
        export_interval_millis=30000,
    )
    meter_provider = MeterProvider(resource=resource, metric_readers=[metric_reader])
    metrics.set_meter_provider(meter_provider)

    tracer = trace.get_tracer(__name__)
    meter = metrics.get_meter(__name__)

    request_counter = meter.create_counter(
        "service_a_requests_total",
        description="Total number of requests to Service A"
    )
    processing_time_histogram = meter.create_histogram(
        "service_a_processing_seconds",
        description="Time spent processing requests in Service A"
    )

    logger.info("OpenTelemetry initialized for Service A with SubtraceIdProcessor")
    return trace_provider


# Initialize OpenTelemetry FIRST
trace_provider = setup_opentelemetry()

# THEN instrument requests - tracer provider must be set first
RequestsInstrumentor().instrument()

# Create FastAPI app
app = FastAPI(
    title="Service A - API Gateway with Subtrace Demo",
    description="API gateway that demonstrates subtrace pattern",
    version="2.0.0"
)

FastAPIInstrumentor.instrument_app(app, tracer_provider=trace_provider)


@app.get("/")
async def root():
    """Health check endpoint"""
    return {"service": "service-a", "status": "healthy", "version": "2.0.0"}


@app.get("/api/process/{customer_id}")
async def process_customer(customer_id: str):
    """
    Process customer data by calling Service B.
    
    This demonstrates the subtrace pattern where:
    - Service A has its own subtrace (all spans in Service A share subtrace.id)
    - Service B has its own subtrace (all spans in Service B share a different subtrace.id)
    - Both are part of the same distributed trace
    
    Args:
        customer_id: Customer ID to process (e.g., user123, user456, user789)
        
    Returns:
        Processed customer data from Service B including:
        - Customer info with loyalty status
        - Payment processing result (may include handled exceptions)
        - DB call statistics
    """
    start_time = time.time()
    
    with tracer.start_as_current_span("process-customer-request") as span:
        span.set_attribute("customer.id", customer_id)
        span.set_attribute("request.type", "customer_processing")
        
        try:
            # Pre-processing in Service A
            with tracer.start_as_current_span("validate-request") as validate_span:
                validate_span.set_attribute("validation.type", "customer_id")
                
                # Simulate validation
                time.sleep(random.uniform(0.01, 0.05))
                
                if not customer_id.startswith("user"):
                    validate_span.set_attribute("validation.result", "invalid_format")
                    raise HTTPException(
                        status_code=400, 
                        detail=f"Invalid customer ID format: {customer_id}"
                    )
                
                validate_span.set_attribute("validation.result", "valid")
            
            # Call Service B for data processing
            service_b_url = os.getenv("SERVICE_B_URL", "http://localhost:8002")
            endpoint = f"/api/data/{customer_id}"
            
            with tracer.start_as_current_span("call-service-b") as call_span:
                call_span.set_attribute("downstream.service", "service-b")
                call_span.set_attribute("downstream.url", f"{service_b_url}{endpoint}")
                
                logger.info(f"Calling Service B at {service_b_url}{endpoint}")
                
                response = requests.get(f"{service_b_url}{endpoint}", timeout=10)
                
                call_span.set_attribute("downstream.status_code", response.status_code)
                call_span.set_attribute("downstream.response_time_ms", response.elapsed.total_seconds() * 1000)
                
                if response.status_code == 404:
                    raise HTTPException(status_code=404, detail=f"Customer {customer_id} not found")
                
                response.raise_for_status()
                result = response.json()
            
            # Post-processing in Service A
            with tracer.start_as_current_span("post-process-response") as post_span:
                time.sleep(random.uniform(0.01, 0.03))
                
                # Extract key info for logging/metrics
                payment_result = result.get("payment_result", {})
                payment_status = payment_result.get("status") if payment_result else "no_payment"
                
                post_span.set_attribute("payment.status", payment_status)
                post_span.set_attribute("response.has_customer", "customer" in result)
                
                # Check if there was a payment failure (Use Case 1 demonstration)
                if payment_status == "failed":
                    post_span.set_attribute("payment.failure_detected", True)
                    post_span.set_attribute("payment.failure_reason", payment_result.get("reason", "unknown"))
                    logger.warning(f"Payment failure detected for customer {customer_id}: {payment_result.get('reason')}")
            
            # Record metrics
            processing_time = time.time() - start_time
            request_counter.add(1, {"endpoint": "/api/process", "status": "success"})
            processing_time_histogram.record(processing_time, {"endpoint": "/api/process"})
            
            span.set_attribute("processing.total_time_ms", processing_time * 1000)
            span.set_attribute("processing.success", True)
            
            return {
                "service": "service-a",
                "customer_id": customer_id,
                "result": result,
                "processing_time_a_ms": processing_time * 1000,
                "subtrace_info": "Each service has its own subtrace.id within the distributed trace"
            }
            
        except HTTPException:
            request_counter.add(1, {"endpoint": "/api/process", "status": "client_error"})
            raise
        except requests.exceptions.Timeout:
            logger.error(f"Timeout calling Service B for customer {customer_id}")
            span.set_attribute("error", True)
            span.set_attribute("error.type", "timeout")
            request_counter.add(1, {"endpoint": "/api/process", "status": "timeout"})
            raise HTTPException(status_code=504, detail="Service B timeout")
        except requests.exceptions.RequestException as e:
            logger.error(f"Error calling Service B: {e}")
            span.set_attribute("error", True)
            span.set_attribute("error.type", "connection_error")
            span.set_attribute("error.message", str(e))
            request_counter.add(1, {"endpoint": "/api/process", "status": "error"})
            raise HTTPException(status_code=503, detail=f"Service B unavailable: {str(e)}")
        except Exception as e:
            logger.error(f"Unexpected error: {e}")
            span.record_exception(e)
            request_counter.add(1, {"endpoint": "/api/process", "status": "error"})
            raise HTTPException(status_code=500, detail=str(e))


@app.get("/api/health")
async def health_check():
    """Detailed health check including Service B connectivity"""
    service_b_url = os.getenv("SERVICE_B_URL", "http://localhost:8002")
    
    try:
        response = requests.get(f"{service_b_url}/api/health", timeout=2)
        service_b_status = response.json() if response.status_code == 200 else {"status": "error"}
    except Exception as e:
        service_b_status = {"status": "unreachable", "error": str(e)}
    
    return {
        "service": "service-a",
        "status": "healthy",
        "version": "2.0.0",
        "features": ["subtrace_id_processor"],
        "dependencies": {
            "service-b": service_b_status
        }
    }


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8001, log_level="info")
