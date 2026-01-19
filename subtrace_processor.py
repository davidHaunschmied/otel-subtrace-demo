"""
SubtraceIdProcessor - Assigns subtrace IDs to spans.

Simple processor that:
- Generates subtrace.id from trace_id + first span_id in this service
- Marks first span as subtrace.is_root_span = true
"""

import hashlib
import threading
from typing import Optional, Dict

from opentelemetry.context import Context
from opentelemetry.sdk.trace import SpanProcessor, ReadableSpan
from opentelemetry.trace import Span


class SubtraceIdProcessor(SpanProcessor):
    """Assigns subtrace IDs to spans. Does NOT modify context."""
    
    def __init__(self):
        self._subtrace_map: Dict[int, tuple] = {}  # trace_id -> (subtrace_id, root_span_id)
    
    def on_start(self, span: Span, parent_context: Optional[Context] = None) -> None:
        try:
            ctx = span.get_span_context()
            trace_id, span_id = ctx.trace_id, ctx.span_id
            
            if trace_id in self._subtrace_map:
                subtrace_id, _ = self._subtrace_map[trace_id]
                # Don't set is_root_span=false - absence means non-root
            else:
                subtrace_id = self._generate_subtrace_id(trace_id, span_id)
                self._subtrace_map[trace_id] = (subtrace_id, span_id)
                span.set_attribute("subtrace.is_root_span", True)
            
            span.set_attribute("subtrace.id", subtrace_id)
        except Exception:
            pass  # Don't break tracing if processor fails
    
    def on_end(self, span: ReadableSpan) -> None:
        pass
    
    def shutdown(self) -> None:
        self._subtrace_map.clear()
    
    def force_flush(self, timeout_millis: int = 30000) -> bool:
        return True
    
    def _generate_subtrace_id(self, trace_id: int, span_id: int) -> str:
        """
        Generate a unique 64-bit subtrace ID from trace_id and span_id.
        
        The subtrace ID is deterministic: same trace_id + span_id always
        produces the same subtrace_id.
        
        Args:
            trace_id: 128-bit trace ID
            span_id: 64-bit span ID
            
        Returns:
            16-character hex string representing 64-bit subtrace ID
        """
        # Combine trace_id and span_id into a string
        combined = f"{trace_id:032x}{span_id:016x}"
        
        # Hash and take first 8 bytes (64 bits)
        hash_bytes = hashlib.sha256(combined.encode()).digest()[:8]
        
        return hash_bytes.hex()


