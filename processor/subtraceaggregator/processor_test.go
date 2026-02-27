package subtraceaggregator

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestShouldStartNewSubtrace(t *testing.T) {
	p := &subtraceProcessor{}

	tests := []struct {
		name       string
		parentKind ptrace.SpanKind
		childKind  ptrace.SpanKind
		sameRes    bool
		want       bool
	}{
		{"client->server same resource", ptrace.SpanKindClient, ptrace.SpanKindServer, true, true},
		{"producer->consumer same resource", ptrace.SpanKindProducer, ptrace.SpanKindConsumer, true, true},
		{"server->server same resource (internal routing)", ptrace.SpanKindServer, ptrace.SpanKindServer, true, false},
		{"consumer->consumer same resource", ptrace.SpanKindConsumer, ptrace.SpanKindConsumer, true, false},
		{"server->internal same resource", ptrace.SpanKindServer, ptrace.SpanKindInternal, true, false},
		{"internal->internal same resource", ptrace.SpanKindInternal, ptrace.SpanKindInternal, true, false},
		{"client->client same resource", ptrace.SpanKindClient, ptrace.SpanKindClient, true, false},
		{"internal->server same resource", ptrace.SpanKindInternal, ptrace.SpanKindServer, true, true},
		{"unspecified->server same resource", ptrace.SpanKindUnspecified, ptrace.SpanKindServer, true, true},
		{"client->unspecified same resource", ptrace.SpanKindClient, ptrace.SpanKindUnspecified, true, false},
		{"any->any different resource", ptrace.SpanKindInternal, ptrace.SpanKindInternal, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := createSpanEntry(tt.parentKind, "res1")
			child := createSpanEntry(tt.childKind, "res1")
			if !tt.sameRes {
				child.ResourceHash = "res2"
			}
			got := p.shouldStartNewSubtrace(parent, child)
			if got != tt.want {
				t.Errorf("shouldStartNewSubtrace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAssignSubtraces_SimpleChain(t *testing.T) {
	p := &subtraceProcessor{}
	traceID := pcommon.TraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

	// A:server -> A:client -> B:server -> B:internal
	spans := []SpanEntry{
		createSpanEntryWithIDs("A-server", "span1", "", ptrace.SpanKindServer, "resA"),
		createSpanEntryWithIDs("A-client", "span2", "span1", ptrace.SpanKindClient, "resA"),
		createSpanEntryWithIDs("B-server", "span3", "span2", ptrace.SpanKindServer, "resB"),
		createSpanEntryWithIDs("B-internal", "span4", "span3", ptrace.SpanKindInternal, "resB"),
	}

	traceState := &TraceState{Spans: spans, FirstSeen: time.Now()}
	subtraces := p.assignSubtraces(traceState, traceID)

	if len(subtraces) != 2 {
		t.Errorf("expected 2 subtraces, got %d", len(subtraces))
	}

	// Verify spans are grouped correctly
	subtraceSizes := make(map[int]int)
	for _, st := range subtraces {
		subtraceSizes[len(st.Spans)]++
	}
	if subtraceSizes[2] != 2 {
		t.Errorf("expected 2 subtraces with 2 spans each, got sizes: %v", subtraceSizes)
	}
}

func TestAssignSubtraces_ServiceCalledTwice(t *testing.T) {
	p := &subtraceProcessor{}
	traceID := pcommon.TraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

	// A:server -> A:client -> B:server (call 1)
	//          -> A:internal
	//          -> A:client -> B:server (call 2)
	spans := []SpanEntry{
		createSpanEntryWithIDs("A-server", "span1", "", ptrace.SpanKindServer, "resA"),
		createSpanEntryWithIDs("A-client-1", "span2", "span1", ptrace.SpanKindClient, "resA"),
		createSpanEntryWithIDs("B-server-1", "span3", "span2", ptrace.SpanKindServer, "resB"),
		createSpanEntryWithIDs("A-internal", "span4", "span1", ptrace.SpanKindInternal, "resA"),
		createSpanEntryWithIDs("A-client-2", "span5", "span1", ptrace.SpanKindClient, "resA"),
		createSpanEntryWithIDs("B-server-2", "span6", "span5", ptrace.SpanKindServer, "resB"),
	}

	traceState := &TraceState{Spans: spans, FirstSeen: time.Now()}
	subtraces := p.assignSubtraces(traceState, traceID)

	if len(subtraces) != 3 {
		t.Errorf("expected 3 subtraces (A + B-call1 + B-call2), got %d", len(subtraces))
	}
}

func TestAssignSubtraces_SelfCallingService(t *testing.T) {
	p := &subtraceProcessor{}
	traceID := pcommon.TraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

	// A:server -> A:client -> A:server (recursive call)
	spans := []SpanEntry{
		createSpanEntryWithIDs("A-server-1", "span1", "", ptrace.SpanKindServer, "resA"),
		createSpanEntryWithIDs("A-client", "span2", "span1", ptrace.SpanKindClient, "resA"),
		createSpanEntryWithIDs("A-server-2", "span3", "span2", ptrace.SpanKindServer, "resA"),
		createSpanEntryWithIDs("A-internal", "span4", "span3", ptrace.SpanKindInternal, "resA"),
	}

	traceState := &TraceState{Spans: spans, FirstSeen: time.Now()}
	subtraces := p.assignSubtraces(traceState, traceID)

	if len(subtraces) != 2 {
		t.Errorf("expected 2 subtraces for self-calling service, got %d", len(subtraces))
	}
}

func TestAssignSubtraces_OrphanSpans(t *testing.T) {
	p := &subtraceProcessor{}
	traceID := pcommon.TraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

	// Two orphan spans (no parent in trace)
	spans := []SpanEntry{
		createSpanEntryWithIDs("orphan-1", "span1", "", ptrace.SpanKindServer, "resA"),
		createSpanEntryWithIDs("orphan-2", "span2", "missing-parent", ptrace.SpanKindServer, "resB"),
	}

	traceState := &TraceState{Spans: spans, FirstSeen: time.Now()}
	subtraces := p.assignSubtraces(traceState, traceID)

	if len(subtraces) != 2 {
		t.Errorf("expected 2 subtraces for orphan spans, got %d", len(subtraces))
	}
}

func TestAssignSubtraces_OutOfOrderArrival(t *testing.T) {
	p := &subtraceProcessor{}
	traceID := pcommon.TraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

	// Spans arrive in reverse order (child before parent)
	spans := []SpanEntry{
		createSpanEntryWithIDs("B-internal", "span4", "span3", ptrace.SpanKindInternal, "resB"),
		createSpanEntryWithIDs("B-server", "span3", "span2", ptrace.SpanKindServer, "resB"),
		createSpanEntryWithIDs("A-client", "span2", "span1", ptrace.SpanKindClient, "resA"),
		createSpanEntryWithIDs("A-server", "span1", "", ptrace.SpanKindServer, "resA"),
	}

	traceState := &TraceState{Spans: spans, FirstSeen: time.Now()}
	subtraces := p.assignSubtraces(traceState, traceID)

	if len(subtraces) != 2 {
		t.Errorf("expected 2 subtraces even with out-of-order arrival, got %d", len(subtraces))
	}
}

func TestAssignSubtraces_InternalRouting(t *testing.T) {
	p := &subtraceProcessor{}
	traceID := pcommon.TraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

	// Server -> Server (internal routing, same subtrace)
	spans := []SpanEntry{
		createSpanEntryWithIDs("gateway-server", "span1", "", ptrace.SpanKindServer, "resA"),
		createSpanEntryWithIDs("handler-server", "span2", "span1", ptrace.SpanKindServer, "resA"),
		createSpanEntryWithIDs("internal-work", "span3", "span2", ptrace.SpanKindInternal, "resA"),
	}

	traceState := &TraceState{Spans: spans, FirstSeen: time.Now()}
	subtraces := p.assignSubtraces(traceState, traceID)

	if len(subtraces) != 1 {
		t.Errorf("expected 1 subtrace for internal routing, got %d", len(subtraces))
	}
	if len(subtraces[0].Spans) != 3 {
		t.Errorf("expected all 3 spans in same subtrace, got %d", len(subtraces[0].Spans))
	}
}

func TestAssignSubtraces_ProducerConsumer(t *testing.T) {
	p := &subtraceProcessor{}
	traceID := pcommon.TraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

	// A:server -> A:producer -> B:consumer -> B:internal
	spans := []SpanEntry{
		createSpanEntryWithIDs("A-server", "span1", "", ptrace.SpanKindServer, "resA"),
		createSpanEntryWithIDs("A-producer", "span2", "span1", ptrace.SpanKindProducer, "resA"),
		createSpanEntryWithIDs("B-consumer", "span3", "span2", ptrace.SpanKindConsumer, "resB"),
		createSpanEntryWithIDs("B-internal", "span4", "span3", ptrace.SpanKindInternal, "resB"),
	}

	traceState := &TraceState{Spans: spans, FirstSeen: time.Now()}
	subtraces := p.assignSubtraces(traceState, traceID)

	if len(subtraces) != 2 {
		t.Errorf("expected 2 subtraces for producer/consumer, got %d", len(subtraces))
	}
}

func TestAssignSubtraces_SameServiceChain(t *testing.T) {
	p := &subtraceProcessor{}
	traceID := pcommon.TraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

	// Reported bug: server -> internal -> client -> client should all be same subtrace
	spans := []SpanEntry{
		createSpanEntryWithIDs("server", "span1", "", ptrace.SpanKindServer, "resA"),
		createSpanEntryWithIDs("internal", "span2", "span1", ptrace.SpanKindInternal, "resA"),
		createSpanEntryWithIDs("client1", "span3", "span2", ptrace.SpanKindClient, "resA"),
		createSpanEntryWithIDs("client2", "span4", "span3", ptrace.SpanKindClient, "resA"),
	}

	traceState := &TraceState{Spans: spans, FirstSeen: time.Now()}
	subtraces := p.assignSubtraces(traceState, traceID)

	if len(subtraces) != 1 {
		t.Errorf("expected 1 subtrace for same-service chain, got %d", len(subtraces))
	}
	if len(subtraces[0].Spans) != 4 {
		t.Errorf("expected all 4 spans in same subtrace, got %d", len(subtraces[0].Spans))
	}
}

// Helper functions

func createSpanEntry(kind ptrace.SpanKind, resourceHash string) *SpanEntry {
	span := ptrace.NewSpan()
	span.SetKind(kind)
	return &SpanEntry{
		Span:         span,
		ResourceHash: resourceHash,
	}
}

func createSpanEntryWithIDs(name, spanID, parentID string, kind ptrace.SpanKind, resourceHash string) SpanEntry {
	span := ptrace.NewSpan()
	span.SetName(name)
	span.SetKind(kind)

	var sid pcommon.SpanID
	copy(sid[:], []byte(spanID))
	span.SetSpanID(sid)

	if parentID != "" {
		var pid pcommon.SpanID
		copy(pid[:], []byte(parentID))
		span.SetParentSpanID(pid)
	}

	return SpanEntry{
		Span:         span,
		Resource:     ptrace.NewResourceSpans(),
		Scope:        ptrace.NewScopeSpans(),
		ResourceHash: resourceHash,
	}
}
