#!/usr/bin/env python3
"""
Test script to generate traffic for the OpenTelemetry demo services
"""

import time
import random
import requests
import threading
from concurrent.futures import ThreadPoolExecutor

BASE_URL_A = "http://localhost:8001"
BASE_URL_B = "http://localhost:8002"

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
            print(f"✓ Processed {data_id}: {response.json()['processing_time_a']:.3f}s")
        except Exception as e:
            print(f"✗ Failed to process {data_id}: {e}")

def test_error_scenarios():
    """Test error scenarios"""
    print("\nTesting error scenarios...")
    
    # Test non-existent data
    try:
        response = requests.get(f"{BASE_URL_A}/api/process/nonexistent")
        print(f"✗ Should have failed: {response.status_code}")
    except requests.exceptions.HTTPError as e:
        print(f"✓ Correctly handled non-existent data: {e}")
    except Exception as e:
        print(f"✓ Non-existent data test: {e}")
    
    # Test invalid data creation
    try:
        response = requests.post(f"{BASE_URL_B}/api/data/user123", json={})
        print(f"✗ Should have failed: {response.status_code}")
    except requests.exceptions.HTTPError as e:
        print(f"✓ Correctly handled duplicate data: {e}")
    except Exception as e:
        print(f"✓ Duplicate data test: {e}")

def create_test_data():
    """Create some test data"""
    print("\nCreating test data...")
    
    test_data = [
        {"name": "Alice Wilson", "email": "alice@example.com", "age": 28},
        {"name": "Bob Brown", "email": "bob@example.com", "age": 35},
        {"name": "Charlie Davis", "email": "charlie@example.com", "age": 42},
    ]
    
    for i, data in enumerate(test_data):
        data_id = f"testuser{i+1}"
        try:
            response = requests.post(f"{BASE_URL_B}/api/data/{data_id}", json=data)
            print(f"✓ Created {data_id}")
        except Exception as e:
            print(f"✗ Failed to create {data_id}: {e}")

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

def list_data():
    """List all data in Service B"""
    print("\nListing data in Service B...")
    
    try:
        response = requests.get(f"{BASE_URL_B}/api/data")
        data = response.json()
        print(f"Found {data['data_count']} data entries:")
        for data_id in data['data_ids']:
            print(f"  - {data_id}")
    except Exception as e:
        print(f"Failed to list data: {e}")

def main():
    """Main test function"""
    print("🚀 OpenTelemetry Demo Test Script")
    print("=" * 50)
    
    # Wait a bit for services to be ready
    print("Waiting for services to be ready...")
    time.sleep(2)
    
    # Run tests
    test_health()
    create_test_data()
    list_data()
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
    print("  Jaeger: http://localhost:16686")
    print("  Zipkin: http://localhost:9411")
    print("  Prometheus: http://localhost:9090")

if __name__ == "__main__":
    main()
