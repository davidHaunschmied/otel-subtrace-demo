package subtraceaggregator

import (
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
)

// SubtraceState holds buffered spans for a single subtrace.
type SubtraceState struct {
	Spans     []ptrace.Span
	RootSpan  *ptrace.Span
	Resource  ptrace.ResourceSpans
	Scope     ptrace.ScopeSpans
	FirstSeen time.Time
}

// Buffer manages subtrace states keyed by subtrace.id.
type Buffer struct {
	mu        sync.RWMutex
	subtraces map[string]*SubtraceState
	maxSpans  int
}

// NewBuffer creates a new subtrace buffer.
func NewBuffer(maxSpansPerSubtrace int) *Buffer {
	return &Buffer{
		subtraces: make(map[string]*SubtraceState),
		maxSpans:  maxSpansPerSubtrace,
	}
}

// Add adds a span to the buffer. Returns true if subtrace should be flushed (max spans reached).
func (b *Buffer) Add(subtraceID string, span ptrace.Span, resource ptrace.ResourceSpans, scope ptrace.ScopeSpans) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	state, exists := b.subtraces[subtraceID]
	if !exists {
		state = &SubtraceState{
			Spans:     make([]ptrace.Span, 0),
			FirstSeen: time.Now(),
			Resource:  resource,
			Scope:     scope,
		}
		b.subtraces[subtraceID] = state
	}

	// Copy span to buffer
	state.Spans = append(state.Spans, span)

	// Check if this is the root span
	if isRootSpan, ok := span.Attributes().Get("subtrace.is_root_span"); ok && isRootSpan.Bool() {
		lastSpan := &state.Spans[len(state.Spans)-1]
		state.RootSpan = lastSpan
	}

	return len(state.Spans) >= b.maxSpans
}

// Get returns the subtrace state for a given ID.
func (b *Buffer) Get(subtraceID string) (*SubtraceState, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	state, exists := b.subtraces[subtraceID]
	return state, exists
}

// Remove removes and returns a subtrace from the buffer.
func (b *Buffer) Remove(subtraceID string) *SubtraceState {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.subtraces[subtraceID]
	delete(b.subtraces, subtraceID)
	return state
}

// GetExpired returns IDs of subtraces that have exceeded the timeout.
func (b *Buffer) GetExpired(timeout time.Duration) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var expired []string
	cutoff := time.Now().Add(-timeout)
	for id, state := range b.subtraces {
		if state.FirstSeen.Before(cutoff) {
			expired = append(expired, id)
		}
	}
	return expired
}

// Size returns the number of buffered subtraces.
func (b *Buffer) Size() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subtraces)
}

// TotalSpans returns the total number of buffered spans across all subtraces.
func (b *Buffer) TotalSpans() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	total := 0
	for _, state := range b.subtraces {
		total += len(state.Spans)
	}
	return total
}

// GetAllIDs returns all subtrace IDs in the buffer.
func (b *Buffer) GetAllIDs() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ids := make([]string, 0, len(b.subtraces))
	for id := range b.subtraces {
		ids = append(ids, id)
	}
	return ids
}
