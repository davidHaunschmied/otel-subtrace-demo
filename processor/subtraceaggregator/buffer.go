package subtraceaggregator

import (
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// SpanEntry holds a span along with its resource and scope context.
type SpanEntry struct {
	Span         ptrace.Span
	Resource     ptrace.ResourceSpans
	Scope        ptrace.ScopeSpans
	ResourceHash string
}

// SubtraceState holds buffered spans for a single subtrace.
type SubtraceState struct {
	Spans      []SpanEntry
	RootSpan   *SpanEntry
	SubtraceID string
	TraceID    pcommon.TraceID
	FirstSeen  time.Time
}

// TraceState holds all spans for a single trace before subtrace assignment.
type TraceState struct {
	Spans     []SpanEntry
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

// Add adds a span to the buffer. Returns true if trace should be flushed (max spans reached).
func (b *Buffer) Add(traceID pcommon.TraceID, resourceHash string, span ptrace.Span, resource ptrace.ResourceSpans, scope ptrace.ScopeSpans) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	traceIDStr := traceID.String()

	traceState, exists := b.traces[traceIDStr]
	if !exists {
		traceState = &TraceState{
			Spans:     make([]SpanEntry, 0),
			FirstSeen: time.Now(),
		}
		b.traces[traceIDStr] = traceState
	}

	// Deep-copy span data to avoid referencing recycled memory
	newSpan := ptrace.NewSpan()
	span.CopyTo(newSpan)

	newResource := ptrace.NewResourceSpans()
	resource.Resource().CopyTo(newResource.Resource())

	newScope := ptrace.NewScopeSpans()
	scope.Scope().CopyTo(newScope.Scope())

	traceState.Spans = append(traceState.Spans, SpanEntry{
		Span:         newSpan,
		Resource:     newResource,
		Scope:        newScope,
		ResourceHash: resourceHash,
	})

	return len(traceState.Spans) >= b.maxSpans
}

// RemoveTrace removes and returns all spans for a trace.
func (b *Buffer) RemoveTrace(traceID pcommon.TraceID) *TraceState {
	b.mu.Lock()
	defer b.mu.Unlock()

	traceIDStr := traceID.String()
	traceState := b.traces[traceIDStr]
	delete(b.traces, traceIDStr)
	return traceState
}

// GetExpiredTraceIDs returns trace IDs that have exceeded the timeout.
func (b *Buffer) GetExpiredTraceIDs(timeout time.Duration) []pcommon.TraceID {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var expired []pcommon.TraceID
	cutoff := time.Now().Add(-timeout)

	for _, traceState := range b.traces {
		if traceState.FirstSeen.Before(cutoff) && len(traceState.Spans) > 0 {
			expired = append(expired, traceState.Spans[0].Span.TraceID())
		}
	}
	return expired
}

// GetAllTraceIDs returns all trace IDs in the buffer.
func (b *Buffer) GetAllTraceIDs() []pcommon.TraceID {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var traceIDs []pcommon.TraceID
	for _, traceState := range b.traces {
		if len(traceState.Spans) > 0 {
			traceIDs = append(traceIDs, traceState.Spans[0].Span.TraceID())
		}
	}
	return traceIDs
}
