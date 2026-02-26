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
		buffer:       NewBuffer(cfg.MaxSpansPerSubtrace),
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
		zap.Int("max_spans_per_subtrace", p.config.MaxSpansPerSubtrace))
	return nil
}

func (p *subtraceProcessor) Shutdown(ctx context.Context) error {
	close(p.shutdownCh)
	p.wg.Wait()

	// Flush all remaining subtraces
	for _, state := range p.buffer.GetAllSubtraces() {
		if err := p.flushSubtrace(ctx, state.TraceID, state.ResourceHash); err != nil {
			p.logger.Error("failed to flush subtrace on shutdown",
				zap.String("subtrace_id", state.SubtraceID),
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
	var toFlush []struct {
		TraceID      pcommon.TraceID
		ResourceHash string
	}

	resourceSpans := td.ResourceSpans()
	for i := 0; i < resourceSpans.Len(); i++ {
		rs := resourceSpans.At(i)
		resource := rs.Resource()

		// Calculate resource hash for subtrace grouping
		resourceHash := p.hashResourceAttributes(resource.Attributes())

		scopeSpans := rs.ScopeSpans()
		for j := 0; j < scopeSpans.Len(); j++ {
			ss := scopeSpans.At(j)
			spans := ss.Spans()
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)
				traceID := span.TraceID()

				// Calculate subtrace ID as hash(traceID, resourceAttributes)
				subtraceID := p.calculateSubtraceID(traceID, resourceHash)

				// Add to buffer
				_, shouldFlush := p.buffer.Add(traceID, resourceHash, subtraceID, span, rs, ss)
				if shouldFlush {
					toFlush = append(toFlush, struct {
						TraceID      pcommon.TraceID
						ResourceHash string
					}{TraceID: traceID, ResourceHash: resourceHash})
				}
			}
		}
	}

	// Flush subtraces that hit max spans
	for _, item := range toFlush {
		if err := p.flushSubtrace(ctx, item.TraceID, item.ResourceHash); err != nil {
			p.logger.Error("failed to flush subtrace",
				zap.String("trace_id", item.TraceID.String()),
				zap.String("resource_hash", item.ResourceHash),
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
			expired := p.buffer.GetExpiredSubtraces(p.config.Timeout)
			for _, item := range expired {
				if err := p.flushSubtrace(context.Background(), item.TraceID, item.ResourceHash); err != nil {
					p.logger.Error("failed to flush expired subtrace",
						zap.String("trace_id", item.TraceID.String()),
						zap.String("resource_hash", item.ResourceHash),
						zap.Error(err))
				}
			}
		}
	}
}

func (p *subtraceProcessor) flushSubtrace(ctx context.Context, traceID pcommon.TraceID, resourceHash string) error {
	state := p.buffer.RemoveSubtrace(traceID, resourceHash)
	if state == nil {
		return nil
	}

	// Find the topmost span (root of subtrace) - the one with no parent in this subtrace
	// or the one whose parent is not in this subtrace
	p.determineRootSpan(state)

	// Set subtrace.id and subtrace.is_root_span on all spans
	for i := range state.Spans {
		entry := &state.Spans[i]
		entry.Span.Attributes().PutStr("subtrace.id", state.SubtraceID)

		if state.RootSpan != nil && entry == state.RootSpan {
			entry.Span.Attributes().PutBool("subtrace.is_root_span", true)
		}
	}

	// Apply aggregations
	if state.RootSpan != nil {
		p.aggregator.Apply(state)
		p.logger.Debug("applied aggregations to subtrace",
			zap.String("subtrace_id", state.SubtraceID),
			zap.Int("span_count", len(state.Spans)))
	} else {
		p.logger.Warn("subtrace has no root span, passing through unchanged",
			zap.String("subtrace_id", state.SubtraceID),
			zap.Int("span_count", len(state.Spans)))
	}

	// Build output traces - group by resource and scope
	td := ptrace.NewTraces()

	// Use first span's resource/scope as template (all should be same resource)
	if len(state.Spans) > 0 {
		rs := td.ResourceSpans().AppendEmpty()
		state.Spans[0].Resource.Resource().CopyTo(rs.Resource())

		ss := rs.ScopeSpans().AppendEmpty()
		state.Spans[0].Scope.Scope().CopyTo(ss.Scope())

		for _, entry := range state.Spans {
			entry.Span.CopyTo(ss.Spans().AppendEmpty())
		}
	}

	// Send to next consumer
	return p.nextConsumer.ConsumeTraces(ctx, td)
}

// determineRootSpan finds the topmost span in the subtrace.
// The root span is the one whose parent span ID is not present in this subtrace.
func (p *subtraceProcessor) determineRootSpan(state *SubtraceState) {
	if len(state.Spans) == 0 {
		return
	}

	// Build a set of all span IDs in this subtrace
	spanIDs := make(map[string]bool)
	for _, entry := range state.Spans {
		spanIDs[entry.Span.SpanID().String()] = true
	}

	// Find spans whose parent is not in this subtrace (candidate roots)
	var candidates []int
	for i, entry := range state.Spans {
		parentID := entry.Span.ParentSpanID().String()
		// Empty parent ID (all zeros) or parent not in this subtrace
		if entry.Span.ParentSpanID().IsEmpty() || !spanIDs[parentID] {
			candidates = append(candidates, i)
		}
	}

	if len(candidates) == 0 {
		// Fallback: use the span with earliest start time
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

	// Multiple candidates: pick the one with earliest start time
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

// calculateSubtraceID generates a subtrace ID from trace ID and resource hash.
func (p *subtraceProcessor) calculateSubtraceID(traceID pcommon.TraceID, resourceHash string) string {
	combined := fmt.Sprintf("%s:%s", traceID.String(), resourceHash)
	hash := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(hash[:8]) // 16-character hex string
}
