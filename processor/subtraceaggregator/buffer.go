package subtraceaggregator

import (
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// SpanEntry holds a span along with its resource and scope context.
type SpanEntry struct {
	Span     ptrace.Span
	Resource ptrace.ResourceSpans
	Scope    ptrace.ScopeSpans
}

// SubtraceState holds buffered spans for a single subtrace (same trace + resource attributes).
type SubtraceState struct {
	Spans        []SpanEntry
	RootSpan     *SpanEntry // Will be determined when flushing (topmost span)
	SubtraceID   string     // Calculated hash(traceID, resourceAttributes)
	TraceID      pcommon.TraceID
	ResourceHash string
	FirstSeen    time.Time
}

// TraceState holds all subtraces for a single trace, grouped by resource attributes.
type TraceState struct {
	Subtraces map[string]*SubtraceState // keyed by resource hash
	FirstSeen time.Time
}

// Buffer manages trace states keyed by trace ID.
type Buffer struct {
	mu       sync.RWMutex
	traces   map[string]*TraceState // keyed by trace ID hex string
	maxSpans int
}

// NewBuffer creates a new trace buffer.
func NewBuffer(maxSpansPerSubtrace int) *Buffer {
	return &Buffer{
		traces:   make(map[string]*TraceState),
		maxSpans: maxSpansPerSubtrace,
	}
}

// Add adds a span to the buffer. Returns the subtrace ID if it should be flushed (max spans reached).
func (b *Buffer) Add(traceID pcommon.TraceID, resourceHash string, subtraceID string, span ptrace.Span, resource ptrace.ResourceSpans, scope ptrace.ScopeSpans) (flushSubtraceID string, shouldFlush bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	traceIDStr := traceID.String()

	traceState, exists := b.traces[traceIDStr]
	if !exists {
		traceState = &TraceState{
			Subtraces: make(map[string]*SubtraceState),
			FirstSeen: time.Now(),
		}
		b.traces[traceIDStr] = traceState
	}

	subtraceState, exists := traceState.Subtraces[resourceHash]
	if !exists {
		subtraceState = &SubtraceState{
			Spans:        make([]SpanEntry, 0),
			SubtraceID:   subtraceID,
			TraceID:      traceID,
			ResourceHash: resourceHash,
			FirstSeen:    time.Now(),
		}
		traceState.Subtraces[resourceHash] = subtraceState
	}

	// Add span entry
	subtraceState.Spans = append(subtraceState.Spans, SpanEntry{
		Span:     span,
		Resource: resource,
		Scope:    scope,
	})

	if len(subtraceState.Spans) >= b.maxSpans {
		return subtraceID, true
	}
	return "", false
}

// RemoveSubtrace removes and returns a specific subtrace from the buffer.
func (b *Buffer) RemoveSubtrace(traceID pcommon.TraceID, resourceHash string) *SubtraceState {
	b.mu.Lock()
	defer b.mu.Unlock()

	traceIDStr := traceID.String()
	traceState, exists := b.traces[traceIDStr]
	if !exists {
		return nil
	}

	subtraceState := traceState.Subtraces[resourceHash]
	delete(traceState.Subtraces, resourceHash)

	// Clean up trace if no more subtraces
	if len(traceState.Subtraces) == 0 {
		delete(b.traces, traceIDStr)
	}

	return subtraceState
}

// GetExpiredTraces returns trace IDs that have exceeded the timeout.
// Returns a list of (traceID, resourceHash) pairs for all expired subtraces.
func (b *Buffer) GetExpiredSubtraces(timeout time.Duration) []struct {
	TraceID      pcommon.TraceID
	ResourceHash string
} {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var expired []struct {
		TraceID      pcommon.TraceID
		ResourceHash string
	}
	cutoff := time.Now().Add(-timeout)

	for _, traceState := range b.traces {
		for resourceHash, subtraceState := range traceState.Subtraces {
			if subtraceState.FirstSeen.Before(cutoff) {
				expired = append(expired, struct {
					TraceID      pcommon.TraceID
					ResourceHash string
				}{
					TraceID:      subtraceState.TraceID,
					ResourceHash: resourceHash,
				})
			}
		}
	}
	return expired
}

// GetAllSubtraces returns all subtrace states in the buffer.
func (b *Buffer) GetAllSubtraces() []*SubtraceState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var subtraces []*SubtraceState
	for _, traceState := range b.traces {
		for _, subtraceState := range traceState.Subtraces {
			subtraces = append(subtraces, subtraceState)
		}
	}
	return subtraces
}
