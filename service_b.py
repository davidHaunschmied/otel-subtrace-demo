"""Service B - Data Service demonstrating subtrace use cases."""

import logging
import os
import random
import time

from fastapi import FastAPI, HTTPException
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.sdk.resources import Resource, SERVICE_NAME
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


class PaymentFailedException(Exception):
    def __init__(self, reason: str):
        super().__init__(reason)
        self.reason = reason


CUSTOMERS = {
    "user123": {"name": "John Doe", "loyalty_status": "gold"},
    "user456": {"name": "Jane Smith", "loyalty_status": "platinum"},
    "user789": {"name": "Bob Johnson", "loyalty_status": "silver"},
}

ORDERS = {
    "user123": {"id": "order001", "amount": 150.00, "status": "pending"},
    "user456": {"id": "order002", "amount": 250.00, "status": "completed"},
    "user789": {"id": "order003", "amount": 50.00, "status": "pending"},
}


def setup_opentelemetry():
    resource = Resource(attributes={SERVICE_NAME: "service-b"})
    trace_provider = TracerProvider(resource=resource)
    otel_endpoint = os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
    trace_provider.add_span_processor(BatchSpanProcessor(
        OTLPSpanExporter(endpoint=otel_endpoint, insecure=True)
    ))
    trace.set_tracer_provider(trace_provider)
    return trace_provider


trace_provider = setup_opentelemetry()
tracer = trace.get_tracer(__name__)

app = FastAPI(title="Service B")
FastAPIInstrumentor.instrument_app(app, tracer_provider=trace_provider)


def simulate_db_query(table: str):
    """Simulate DB query - creates span with db.system for N+1 detection."""
    with tracer.start_as_current_span(f"db-query-{table}") as span:
        span.set_attribute("db.system", "postgresql")
        span.set_attribute("db.sql.table", table)
        time.sleep(random.uniform(0.01, 0.03))


def process_payment(order_id: str):
    """Process payment - may fail ~30% of time (exception propagation demo)."""
    with tracer.start_as_current_span("process-payment") as span:
        time.sleep(random.uniform(0.02, 0.05))
        
        if random.random() < 0.3:
            exc = PaymentFailedException(random.choice([
                "insufficient_funds", "card_declined", "fraud_suspected"
            ]))
            span.record_exception(exc)
            return {"status": "failed", "reason": exc.reason}
        
        return {"status": "success", "transaction_id": f"txn_{random.randint(100000, 999999)}"}


@app.get("/")
async def root():
    return {"service": "service-b", "status": "healthy"}


@app.get("/api/data/{customer_id}")
async def get_data(customer_id: str):
    """Main endpoint demonstrating all subtrace use cases."""
    if customer_id not in CUSTOMERS:
        raise HTTPException(status_code=404, detail="Customer not found")
    
    customer = CUSTOMERS[customer_id]
    
    # Use Case 2: Multiple DB calls (N+1 detection)
    for table in ["customers", "preferences", "loyalty", "orders", "addresses"]:
        simulate_db_query(table)
    
    # Use Case 3: Capture loyalty status on child span
    with tracer.start_as_current_span("get-loyalty") as span:
        span.set_attribute("customer.loyalty_status", customer["loyalty_status"])
    
    # Use Case 1: Payment with potential exception
    order = ORDERS.get(customer_id)
    payment_result = None
    if order and order["status"] == "pending":
        payment_result = process_payment(order["id"])
    
    return {
        "customer": customer,
        "order": order,
        "payment_result": payment_result,
    }


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8002)
