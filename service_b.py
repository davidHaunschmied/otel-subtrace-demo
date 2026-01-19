"""
Service B - Data Service with Subtrace Use Cases

Demonstrates three subtrace aggregation use cases:
1. PaymentFailedException on deep child span (exception propagation)
2. Multiple DB calls with random count (N+1 detection)
3. Customer loyalty status capture (deep analytics)
"""

import logging
import os
import random
import time
from typing import Dict, Any, Optional

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

# Configure logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Global variables for telemetry
tracer = None
meter = None
data_processing_counter = None
db_call_counter = None


# Custom Exceptions for Use Case 1
class PaymentFailedException(Exception):
    """Raised when payment processing fails"""
    def __init__(self, message: str, payment_id: str, reason: str):
        super().__init__(message)
        self.payment_id = payment_id
        self.reason = reason


class PaymentServiceUnavailableException(Exception):
    """Raised when payment service is unavailable"""
    pass


# Mock databases
mock_customer_database: Dict[str, Dict[str, Any]] = {
    "user123": {
        "name": "John Doe", 
        "email": "john@example.com", 
        "age": 30,
        "loyalty_status": "gold",
        "payment_method": "credit_card",
        "credit_limit": 5000
    },
    "user456": {
        "name": "Jane Smith", 
        "email": "jane@example.com", 
        "age": 25,
        "loyalty_status": "platinum",
        "payment_method": "debit_card",
        "credit_limit": 10000
    },
    "user789": {
        "name": "Bob Johnson", 
        "email": "bob@example.com", 
        "age": 35,
        "loyalty_status": "silver",
        "payment_method": "paypal",
        "credit_limit": 2000
    },
}

mock_order_database: Dict[str, Dict[str, Any]] = {
    "order001": {"customer_id": "user123", "amount": 150.00, "status": "pending"},
    "order002": {"customer_id": "user456", "amount": 250.00, "status": "completed"},
    "order003": {"customer_id": "user789", "amount": 50.00, "status": "pending"},
}


def setup_opentelemetry():
    """Configure OpenTelemetry tracing and metrics with SubtraceIdProcessor"""
    global tracer, meter, data_processing_counter, db_call_counter

    resource = Resource(attributes={
        SERVICE_NAME: "service-b",
        SERVICE_VERSION: "2.0.0",
        SERVICE_INSTANCE_ID: "service-b-1"
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

    data_processing_counter = meter.create_counter(
        "service_b_data_processing_total",
        description="Total number of data processing operations"
    )
    db_call_counter = meter.create_counter(
        "service_b_db_calls_total",
        description="Total number of database calls"
    )

    logger.info("OpenTelemetry initialized for Service B with SubtraceIdProcessor")
    return trace_provider


# Initialize OpenTelemetry
trace_provider = setup_opentelemetry()

# Create FastAPI app
app = FastAPI(
    title="Service B - Data Service with Subtrace Demo",
    description="Demonstrates subtrace aggregation use cases",
    version="2.0.0"
)

FastAPIInstrumentor.instrument_app(app, tracer_provider=trace_provider)


# =============================================================================
# Database Simulation Functions (Use Case 2: Multiple DB Calls)
# =============================================================================

def simulate_db_query(query_name: str, table: str, operation: str = "SELECT") -> Dict[str, Any]:
    """
    Simulate a database query with tracing.
    Each call creates a span with db.system attribute for counting.
    """
    with tracer.start_as_current_span(f"db-{query_name}") as span:
        # Standard DB semantic conventions
        span.set_attribute("db.system", "postgresql")
        span.set_attribute("db.operation", operation)
        span.set_attribute("db.name", "app_database")
        span.set_attribute("db.sql.table", table)
        span.set_attribute("db.query.name", query_name)
        
        # Simulate latency
        delay = random.uniform(0.01, 0.05)
        time.sleep(delay)
        
        span.set_attribute("db.query.duration_ms", delay * 1000)
        db_call_counter.add(1, {"table": table, "operation": operation})
        
        return {"query": query_name, "duration_ms": delay * 1000}


def get_customer_with_multiple_queries(customer_id: str) -> Optional[Dict[str, Any]]:
    """
    Fetch customer data with multiple DB queries (simulating N+1 pattern).
    The number of queries varies randomly to demonstrate counting.
    """
    with tracer.start_as_current_span("fetch-customer-data") as span:
        span.set_attribute("customer.id", customer_id)
        
        # Query 1: Get basic customer info
        simulate_db_query("get_customer_basic", "customers")
        
        if customer_id not in mock_customer_database:
            return None
        
        customer = mock_customer_database[customer_id].copy()
        
        # Query 2: Get customer preferences
        simulate_db_query("get_customer_preferences", "customer_preferences")
        
        # Query 3: Get loyalty info - THIS CAPTURES LOYALTY STATUS (Use Case 3)
        with tracer.start_as_current_span("get-loyalty-info") as loyalty_span:
            simulate_db_query("get_loyalty_details", "loyalty_program")
            
            # Use Case 3: Capture loyalty status on this child span
            loyalty_status = customer.get("loyalty_status", "standard")
            loyalty_span.set_attribute("customer.loyalty_status", loyalty_status)
            loyalty_span.set_attribute("customer.loyalty_tier", loyalty_status)
            logger.info(f"Captured loyalty status: {loyalty_status} for customer {customer_id}")
        
        # Random additional queries (2-5 more) to vary DB call count
        additional_queries = random.randint(2, 5)
        for i in range(additional_queries):
            query_types = [
                ("get_recent_orders", "orders"),
                ("get_payment_history", "payments"),
                ("get_shipping_addresses", "addresses"),
                ("get_wishlist", "wishlists"),
                ("get_reviews", "reviews"),
            ]
            query_name, table = random.choice(query_types)
            simulate_db_query(f"{query_name}_{i}", table)
        
        span.set_attribute("db.total_queries", 3 + additional_queries)
        
        return customer


# =============================================================================
# Payment Processing (Use Case 1: Exception on Deep Child Span)
# =============================================================================

def process_payment(customer_id: str, amount: float, order_id: str) -> Dict[str, Any]:
    """
    Process payment - may raise PaymentFailedException on deep child span.
    The exception is recorded but handled gracefully.
    """
    with tracer.start_as_current_span("process-payment") as span:
        span.set_attribute("payment.customer_id", customer_id)
        span.set_attribute("payment.amount", amount)
        span.set_attribute("payment.order_id", order_id)
        
        # Simulate payment gateway call
        result = call_payment_gateway(customer_id, amount, order_id)
        
        span.set_attribute("payment.result", result.get("status", "unknown"))
        return result


def call_payment_gateway(customer_id: str, amount: float, order_id: str) -> Dict[str, Any]:
    """
    Call external payment gateway - this is where exceptions may occur.
    """
    with tracer.start_as_current_span("call-payment-gateway") as span:
        span.set_attribute("gateway.name", "stripe")
        span.set_attribute("gateway.timeout_ms", 5000)
        
        # Simulate gateway latency
        time.sleep(random.uniform(0.05, 0.15))
        
        # Validate payment with downstream service
        return validate_payment_with_provider(customer_id, amount, order_id)


def validate_payment_with_provider(customer_id: str, amount: float, order_id: str) -> Dict[str, Any]:
    """
    Deep child span that validates payment - USE CASE 1: Exception occurs here.
    
    Randomly fails ~30% of the time to demonstrate exception propagation.
    The exception is recorded as a span event but handled gracefully.
    """
    with tracer.start_as_current_span("validate-payment-provider") as span:
        span.set_attribute("provider.name", "payment_validator")
        span.set_attribute("validation.customer_id", customer_id)
        span.set_attribute("validation.amount", amount)
        
        time.sleep(random.uniform(0.02, 0.08))
        
        # Simulate random payment failures (30% chance)
        if random.random() < 0.3:
            # USE CASE 1: PaymentFailedException on deep child span
            exception = PaymentFailedException(
                message=f"Payment validation failed for order {order_id}",
                payment_id=f"pay_{order_id}",
                reason=random.choice([
                    "insufficient_funds",
                    "card_declined", 
                    "fraud_suspected",
                    "expired_card"
                ])
            )
            
            # Record exception as span event - this will be propagated to root
            span.record_exception(exception)
            span.set_attribute("payment.failed", True)
            span.set_attribute("payment.failure_reason", exception.reason)
            
            logger.warning(f"Payment failed: {exception.reason} for order {order_id}")
            
            # Handle gracefully - return failure status instead of raising
            return {
                "status": "failed",
                "reason": exception.reason,
                "payment_id": exception.payment_id,
                "fallback": "queued_for_retry"
            }
        
        # Success case
        span.set_attribute("payment.validated", True)
        return {
            "status": "success",
            "payment_id": f"pay_{order_id}_{int(time.time())}",
            "transaction_id": f"txn_{random.randint(100000, 999999)}"
        }


# =============================================================================
# API Endpoints
# =============================================================================

@app.get("/")
async def root():
    """Health check endpoint"""
    return {"service": "service-b", "status": "healthy", "version": "2.0.0"}


@app.get("/api/data/{data_id}")
async def get_data(data_id: str):
    """
    Main endpoint demonstrating all three subtrace use cases:
    1. Payment exception on deep child span
    2. Multiple DB calls (random count)
    3. Loyalty status capture
    """
    start_time = time.time()
    
    with tracer.start_as_current_span("service-b-process-request") as span:
        span.set_attribute("request.data_id", data_id)
        span.set_attribute("request.type", "full_processing")
        
        try:
            # USE CASE 2 & 3: Multiple DB queries + Loyalty status capture
            customer = get_customer_with_multiple_queries(data_id)
            
            if customer is None:
                raise HTTPException(status_code=404, detail=f"Customer {data_id} not found")
            
            # Get associated order for payment processing
            order = None
            for order_id, order_data in mock_order_database.items():
                if order_data["customer_id"] == data_id:
                    order = {"id": order_id, **order_data}
                    break
            
            payment_result = None
            if order and order["status"] == "pending":
                # USE CASE 1: Payment processing with potential exception
                payment_result = process_payment(
                    customer_id=data_id,
                    amount=order["amount"],
                    order_id=order["id"]
                )
            
            total_time = time.time() - start_time
            data_processing_counter.add(1, {"operation": "get", "status": "success"})
            
            return {
                "service": "service-b",
                "customer_id": data_id,
                "customer": customer,
                "order": order,
                "payment_result": payment_result,
                "processing_time_seconds": total_time
            }
            
        except HTTPException:
            data_processing_counter.add(1, {"operation": "get", "status": "not_found"})
            raise
        except Exception as e:
            logger.error(f"Error processing request for {data_id}: {e}")
            data_processing_counter.add(1, {"operation": "get", "status": "error"})
            span.record_exception(e)
            raise HTTPException(status_code=500, detail=str(e))


@app.get("/api/customer/{customer_id}/loyalty")
async def get_loyalty_status(customer_id: str):
    """
    Endpoint specifically for loyalty status lookup.
    Demonstrates Use Case 3: Loyalty status capture.
    """
    with tracer.start_as_current_span("get-loyalty-status") as span:
        span.set_attribute("customer.id", customer_id)
        
        # DB query to get loyalty info
        simulate_db_query("lookup_loyalty", "loyalty_program")
        
        if customer_id not in mock_customer_database:
            raise HTTPException(status_code=404, detail=f"Customer {customer_id} not found")
        
        customer = mock_customer_database[customer_id]
        loyalty_status = customer.get("loyalty_status", "standard")
        
        # Capture loyalty status on span
        span.set_attribute("customer.loyalty_status", loyalty_status)
        
        return {
            "customer_id": customer_id,
            "loyalty_status": loyalty_status,
            "benefits": get_loyalty_benefits(loyalty_status)
        }


def get_loyalty_benefits(status: str) -> Dict[str, Any]:
    """Get benefits for loyalty status"""
    benefits = {
        "standard": {"discount": 0, "free_shipping": False, "priority_support": False},
        "silver": {"discount": 5, "free_shipping": False, "priority_support": False},
        "gold": {"discount": 10, "free_shipping": True, "priority_support": False},
        "platinum": {"discount": 15, "free_shipping": True, "priority_support": True},
    }
    return benefits.get(status, benefits["standard"])


@app.post("/api/payment/process")
async def process_payment_endpoint(customer_id: str, amount: float, order_id: str):
    """
    Direct payment processing endpoint.
    Demonstrates Use Case 1: Exception on deep child span.
    """
    with tracer.start_as_current_span("payment-request") as span:
        span.set_attribute("payment.customer_id", customer_id)
        span.set_attribute("payment.amount", amount)
        span.set_attribute("payment.order_id", order_id)
        
        result = process_payment(customer_id, amount, order_id)
        
        # Even if payment failed, we return 200 (handled gracefully)
        return {
            "service": "service-b",
            "payment": result,
            "note": "Payment failures are handled gracefully and queued for retry"
        }


@app.get("/api/health")
async def health_check():
    """Detailed health check"""
    return {
        "service": "service-b",
        "status": "healthy",
        "version": "2.0.0",
        "features": [
            "subtrace_id_processor",
            "exception_propagation",
            "db_call_counting",
            "loyalty_status_capture"
        ],
        "customer_count": len(mock_customer_database),
        "order_count": len(mock_order_database)
    }


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8002, log_level="info")
