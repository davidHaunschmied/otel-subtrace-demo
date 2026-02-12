#!/usr/bin/env python3
"""
Test script to generate traffic for the OpenTelemetry demo services
"""

import time
import random
import requests
import threading
from concurrent.futures import ThreadPoolExecutor

BASE_URL_A = "http://localhost:18001"
BASE_URL_B = "http://localhost:18002"

def test_health():
    """Test health endpoints"""
    print("Testing health endpoints...")
    
    try:
        response = requests.get(f"{BASE_URL_A}/")
        print(f"Service A health: {response.json()}")
    except Exception as e:
        print(f"Service A health failed: {e}")
    
    try:
        response = requests.get(f"{BASE_URL_B}/")
        print(f"Service B health: {response.json()}")
    except Exception as e:
        print(f"Service B health failed: {e}")

def test_service_communication():
    """Test service A calling service B"""
    print("\nTesting service communication...")
    
    test_ids = ["user123", "user456", "user789"]
    
    for data_id in test_ids:
        try:
            response = requests.get(f"{BASE_URL_A}/api/process/{data_id}")
            data = response.json()
            processing_time = data.get('processing_time_a_ms', 0) / 1000
            result = data.get('result', {})
            payment_result = result.get('payment_result') if result else None
            payment_status = payment_result.get('status', 'N/A') if payment_result else 'no_payment'
            print(f"✓ Processed {data_id}: {processing_time:.3f}s (payment: {payment_status})")
        except Exception as e:
            print(f"✗ Failed to process {data_id}: {e}")

def test_error_scenarios():
    """Test error scenarios"""
    print("\nTesting error scenarios...")
    
    # Test invalid customer ID format
    try:
        response = requests.get(f"{BASE_URL_A}/api/process/invalid_id")
        if response.status_code == 400:
            print(f"✓ Correctly rejected invalid ID format (400)")
        else:
            print(f"? Unexpected status: {response.status_code}")
    except Exception as e:
        print(f"✓ Invalid ID test: {e}")
    
    # Test non-existent customer
    try:
        response = requests.get(f"{BASE_URL_B}/api/data/user999")
        if response.status_code == 404:
            print(f"✓ Correctly returned 404 for non-existent customer")
        else:
            print(f"? Unexpected status: {response.status_code}")
    except Exception as e:
        print(f"✓ Non-existent customer test: {e}")

def test_direct_service_b():
    """Test Service B directly"""
    print("\nTesting Service B directly...")
    
    test_ids = ["user123", "user456", "user789"]
    
    for data_id in test_ids:
        try:
            response = requests.get(f"{BASE_URL_B}/api/data/{data_id}")
            data = response.json()
            loyalty = data.get('customer', {}).get('loyalty_status', 'N/A')
            print(f"✓ Got {data_id}: loyalty={loyalty}")
        except Exception as e:
            print(f"✗ Failed to get {data_id}: {e}")

def generate_load(duration_seconds=60, concurrent_requests=5):
    """Generate load for testing"""
    print(f"\nGenerating load for {duration_seconds} seconds with {concurrent_requests} concurrent requests...")
    
    end_time = time.time() + duration_seconds
    request_count = 0
    error_count = 0
    
    def make_request():
        nonlocal request_count, error_count
        data_ids = ["user123", "user456", "user789", "testuser1", "testuser2"]
        data_id = random.choice(data_ids)
        
        try:
            response = requests.get(f"{BASE_URL_A}/api/process/{data_id}", timeout=5)
            request_count += 1
            if response.status_code != 200:
                error_count += 1
        except Exception:
            error_count += 1
    
    with ThreadPoolExecutor(max_workers=concurrent_requests) as executor:
        while time.time() < end_time:
            futures = [executor.submit(make_request) for _ in range(concurrent_requests)]
            for future in futures:
                future.result()
            time.sleep(0.1)
    
    print(f"Load generation complete: {request_count} requests, {error_count} errors")

def test_loyalty_endpoint():
    """Test loyalty status endpoint"""
    print("\nTesting loyalty endpoint...")
    
    test_ids = ["user123", "user456", "user789"]
    
    for data_id in test_ids:
        try:
            response = requests.get(f"{BASE_URL_B}/api/customer/{data_id}/loyalty")
            data = response.json()
            print(f"✓ {data_id}: {data.get('loyalty_status', 'N/A')} (discount: {data.get('benefits', {}).get('discount', 0)}%)")
        except Exception as e:
            print(f"✗ Failed to get loyalty for {data_id}: {e}")

def main():
    """Main test function"""
    print("🚀 OpenTelemetry Demo Test Script")
    print("=" * 50)
    
    # Wait a bit for services to be ready
    print("Waiting for services to be ready...")
    time.sleep(2)
    
    # Run tests
    test_health()
    test_direct_service_b()
    test_loyalty_endpoint()
    test_service_communication()
    test_error_scenarios()
    
    # Ask user if they want to generate load
    user_input = input("\nDo you want to generate load? (y/N): ").lower().strip()
    if user_input == 'y':
        duration = input("Load duration in seconds (default 60): ").strip()
        duration = int(duration) if duration.isdigit() else 60
        
        concurrent = input("Concurrent requests (default 5): ").strip()
        concurrent = int(concurrent) if concurrent.isdigit() else 5
        
        generate_load(duration, concurrent)
    
    print("\n✅ Test completed!")
    print("\n📊 View telemetry data:")
    print("  Jaeger: http://localhost:26686")
    print("  Prometheus: http://localhost:19090")

if __name__ == "__main__":
    main()
