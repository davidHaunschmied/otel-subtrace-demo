package subtraceaggregator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

type subtraceProcessor struct {
	logger       *zap.Logger
	config       *Config
	nextConsumer consumer.Traces
	buffer       *Buffer
	aggregator   *Aggregator

	shutdownCh chan struct{}
	wg         sync.WaitGroup
}

func newProcessor(logger *zap.Logger, cfg *Config, next consumer.Traces) (*subtraceProcessor, error) {
	p := &subtraceProcessor{
		logger:       logger,
		config:       cfg,
		nextConsumer: next,
		buffer:       NewBuffer(cfg.MaxSpansPerTrace),
		aggregator:   NewAggregator(cfg.AttributeAggregations, cfg.EventAggregations),
		shutdownCh:   make(chan struct{}),
	}
	return p, nil
}

func (p *subtraceProcessor) Start(ctx context.Context, host component.Host) error {
	p.wg.Add(1)
	go p.flushLoop()
	p.logger.Info("subtraceaggregator processor started",
		zap.Duration("timeout", p.config.Timeout),
		zap.Int("max_spans_per_trace", p.config.MaxSpansPerTrace))
	return nil
}

func (p *subtraceProcessor) Shutdown(ctx context.Context) error {
	close(p.shutdownCh)
	p.wg.Wait()

	// Flush all remaining traces
	for _, traceID := range p.buffer.GetAllTraceIDs() {
		if err := p.flushTrace(ctx, traceID); err != nil {
			p.logger.Error("failed to flush trace on shutdown",
				zap.String("trace_id", traceID.String()),
				zap.Error(err))
		}
	}

	p.logger.Info("subtraceaggregator processor shutdown complete")
	return nil
}

func (p *subtraceProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *subtraceProcessor) ConsumeTraces(ctx context.Context, td ptrace.Traces) error {
	var toFlush []pcommon.TraceID

	resourceSpans := td.ResourceSpans()
	for i := 0; i < resourceSpans.Len(); i++ {
		rs := resourceSpans.At(i)
		resource := rs.Resource()
		resourceHash := p.hashResourceAttributes(resource.Attributes())

		scopeSpans := rs.ScopeSpans()
		for j := 0; j < scopeSpans.Len(); j++ {
			ss := scopeSpans.At(j)
			spans := ss.Spans()
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)
				traceID := span.TraceID()

				shouldFlush := p.buffer.Add(traceID, resourceHash, span, rs, ss)
				if shouldFlush {
					toFlush = append(toFlush, traceID)
				}
			}
		}
	}

	for _, traceID := range toFlush {
		if err := p.flushTrace(ctx, traceID); err != nil {
			p.logger.Error("failed to flush trace",
				zap.String("trace_id", traceID.String()),
				zap.Error(err))
		}
	}

	return nil
}

func (p *subtraceProcessor) flushLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.shutdownCh:
			return
		case <-ticker.C:
			for _, traceID := range p.buffer.GetExpiredTraceIDs(p.config.Timeout) {
				if err := p.flushTrace(context.Background(), traceID); err != nil {
					p.logger.Error("failed to flush expired trace",
						zap.String("trace_id", traceID.String()),
						zap.Error(err))
				}
			}
		}
	}
}

func (p *subtraceProcessor) flushTrace(ctx context.Context, traceID pcommon.TraceID) error {
	traceState := p.buffer.RemoveTrace(traceID)
	if traceState == nil || len(traceState.Spans) == 0 {
		return nil
	}

	// Assign spans to subtraces based on parent-child relationships and service boundaries
	subtraces := p.assignSubtraces(traceState, traceID)

	// Process each subtrace
	for _, state := range subtraces {
		p.determineRootSpan(state)

		for i := range state.Spans {
			entry := &state.Spans[i]
			entry.Span.Attributes().PutStr("subtrace.id", state.SubtraceID)
			if state.RootSpan != nil && entry == state.RootSpan {
				entry.Span.Attributes().PutBool("subtrace.is_root_span", true)
			}
		}

		if state.RootSpan != nil {
			p.aggregator.Apply(state)
		}

		// Build and send output
		td := ptrace.NewTraces()
		if len(state.Spans) > 0 {
			rs := td.ResourceSpans().AppendEmpty()
			state.Spans[0].Resource.Resource().CopyTo(rs.Resource())
			ss := rs.ScopeSpans().AppendEmpty()
			state.Spans[0].Scope.Scope().CopyTo(ss.Scope())
			for _, entry := range state.Spans {
				entry.Span.CopyTo(ss.Spans().AppendEmpty())
			}
		}

		if err := p.nextConsumer.ConsumeTraces(ctx, td); err != nil {
			return err
		}
	}

	return nil
}

// assignSubtraces groups spans into subtraces based on parent-child relationships and service boundaries.
func (p *subtraceProcessor) assignSubtraces(traceState *TraceState, traceID pcommon.TraceID) []*SubtraceState {
	spans := traceState.Spans
	if len(spans) == 0 {
		return nil
	}

	// Build span lookup by span ID
	spanByID := make(map[string]*SpanEntry)
	for i := range spans {
		spanByID[spans[i].Span.SpanID().String()] = &spans[i]
	}

	subtraceAssignment := make(map[string]string) // spanID -> subtraceID
	subtraceCounter := &[]int{0}[0]

	// Recursive function to assign subtrace, resolving parents first
	var assignSpan func(span *SpanEntry) string
	assignSpan = func(span *SpanEntry) string {
		spanID := span.Span.SpanID().String()

		if subtrace, assigned := subtraceAssignment[spanID]; assigned {
			return subtrace
		}

		parentID := span.Span.ParentSpanID().String()
		parent, hasParent := spanByID[parentID]

		if !hasParent || span.Span.ParentSpanID().IsEmpty() {
			// Orphan/root span - starts new subtrace
			subtrace := p.generateSubtraceID(traceID, *subtraceCounter)
			*subtraceCounter++
			subtraceAssignment[spanID] = subtrace
			return subtrace
		}

		// Resolve parent's subtrace first
		parentSubtrace := assignSpan(parent)

		if p.shouldStartNewSubtrace(parent, span) {
			// Service boundary - new subtrace
			subtrace := p.generateSubtraceID(traceID, *subtraceCounter)
			*subtraceCounter++
			subtraceAssignment[spanID] = subtrace
			return subtrace
		}

		// Inherit parent's subtrace
		subtraceAssignment[spanID] = parentSubtrace
		return parentSubtrace
	}

	// Assign all spans
	for i := range spans {
		assignSpan(&spans[i])
	}

	// Group spans by subtrace ID
	subtraceMap := make(map[string]*SubtraceState)
	for i := range spans {
		span := &spans[i]
		spanID := span.Span.SpanID().String()
		subtraceID := subtraceAssignment[spanID]

		if _, exists := subtraceMap[subtraceID]; !exists {
			subtraceMap[subtraceID] = &SubtraceState{
				Spans:      make([]SpanEntry, 0),
				SubtraceID: subtraceID,
				TraceID:    traceID,
				FirstSeen:  traceState.FirstSeen,
			}
		}
		subtraceMap[subtraceID].Spans = append(subtraceMap[subtraceID].Spans, *span)
	}

	// Convert to slice
	result := make([]*SubtraceState, 0, len(subtraceMap))
	for _, state := range subtraceMap {
		result = append(result, state)
	}
	return result
}

// shouldStartNewSubtrace determines if a child span should start a new subtrace.
func (p *subtraceProcessor) shouldStartNewSubtrace(parent, child *SpanEntry) bool {
	// Different resource → new subtrace
	if parent.ResourceHash != child.ResourceHash {
		return true
	}
	// Service boundary detection
	childKind := child.Span.Kind()
	parentKind := parent.Span.Kind()
	// Treat UNSPECIFIED as INTERNAL
	if childKind == ptrace.SpanKindUnspecified {
		childKind = ptrace.SpanKindInternal
	}
	if parentKind == ptrace.SpanKindUnspecified {
		parentKind = ptrace.SpanKindInternal
	}
	isChildEntryPoint := childKind == ptrace.SpanKindServer || childKind == ptrace.SpanKindConsumer
	isParentEntryPoint := parentKind == ptrace.SpanKindServer || parentKind == ptrace.SpanKindConsumer
	return isChildEntryPoint && !isParentEntryPoint
}

// generateSubtraceID creates a unique subtrace ID.
func (p *subtraceProcessor) generateSubtraceID(traceID pcommon.TraceID, counter int) string {
	combined := fmt.Sprintf("%s:%d", traceID.String(), counter)
	hash := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(hash[:8])
}

// determineRootSpan finds the topmost span in the subtrace.
func (p *subtraceProcessor) determineRootSpan(state *SubtraceState) {
	if len(state.Spans) == 0 {
		return
	}

	spanIDs := make(map[string]bool)
	for _, entry := range state.Spans {
		spanIDs[entry.Span.SpanID().String()] = true
	}

	var candidates []int
	for i, entry := range state.Spans {
		parentID := entry.Span.ParentSpanID().String()
		if entry.Span.ParentSpanID().IsEmpty() || !spanIDs[parentID] {
			candidates = append(candidates, i)
		}
	}

	if len(candidates) == 0 {
		earliest := 0
		for i, entry := range state.Spans {
			if entry.Span.StartTimestamp() < state.Spans[earliest].Span.StartTimestamp() {
				earliest = i
			}
		}
		state.RootSpan = &state.Spans[earliest]
		return
	}

	if len(candidates) == 1 {
		state.RootSpan = &state.Spans[candidates[0]]
		return
	}

	earliest := candidates[0]
	for _, idx := range candidates[1:] {
		if state.Spans[idx].Span.StartTimestamp() < state.Spans[earliest].Span.StartTimestamp() {
			earliest = idx
		}
	}
	state.RootSpan = &state.Spans[earliest]
}

// hashResourceAttributes creates a deterministic hash of resource attributes.
func (p *subtraceProcessor) hashResourceAttributes(attrs pcommon.Map) string {
	// Sort keys for deterministic ordering
	keys := make([]string, 0, attrs.Len())
	attrs.Range(func(k string, v pcommon.Value) bool {
		keys = append(keys, k)
		return true
	})
	sort.Strings(keys)

	// Build canonical string representation
	var builder string
	for _, k := range keys {
		v, _ := attrs.Get(k)
		builder += fmt.Sprintf("%s=%s;", k, v.AsString())
	}

	// Hash it
	hash := sha256.Sum256([]byte(builder))
	return hex.EncodeToString(hash[:8]) // Use first 8 bytes (64 bits)
}

