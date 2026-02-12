package subtraceaggregator

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
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
	for _, id := range p.buffer.GetAllIDs() {
		if err := p.flushSubtrace(ctx, id); err != nil {
			p.logger.Error("failed to flush subtrace on shutdown", zap.String("subtrace_id", id), zap.Error(err))
		}
	}

	p.logger.Info("subtraceaggregator processor shutdown complete")
	return nil
}

func (p *subtraceProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *subtraceProcessor) ConsumeTraces(ctx context.Context, td ptrace.Traces) error {
	var toFlush []string

	resourceSpans := td.ResourceSpans()
	for i := 0; i < resourceSpans.Len(); i++ {
		rs := resourceSpans.At(i)
		scopeSpans := rs.ScopeSpans()
		for j := 0; j < scopeSpans.Len(); j++ {
			ss := scopeSpans.At(j)
			spans := ss.Spans()
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)

				// Get subtrace.id
				subtraceIDVal, exists := span.Attributes().Get("subtrace.id")
				if !exists {
					// No subtrace.id - pass through immediately
					continue
				}
				subtraceID := subtraceIDVal.Str()

				// Add to buffer
				shouldFlush := p.buffer.Add(subtraceID, span, rs, ss)
				if shouldFlush {
					toFlush = append(toFlush, subtraceID)
				}
			}
		}
	}

	// Flush subtraces that hit max spans
	for _, id := range toFlush {
		if err := p.flushSubtrace(ctx, id); err != nil {
			p.logger.Error("failed to flush subtrace", zap.String("subtrace_id", id), zap.Error(err))
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
			expired := p.buffer.GetExpired(p.config.Timeout)
			for _, id := range expired {
				if err := p.flushSubtrace(context.Background(), id); err != nil {
					p.logger.Error("failed to flush expired subtrace", zap.String("subtrace_id", id), zap.Error(err))
				}
			}
		}
	}
}

func (p *subtraceProcessor) flushSubtrace(ctx context.Context, subtraceID string) error {
	state := p.buffer.Remove(subtraceID)
	if state == nil {
		return nil
	}

	// Apply aggregations
	if state.RootSpan != nil {
		p.aggregator.Apply(state)
		p.logger.Debug("applied aggregations to subtrace",
			zap.String("subtrace_id", subtraceID),
			zap.Int("span_count", len(state.Spans)))
	} else {
		p.logger.Warn("subtrace has no root span, passing through unchanged",
			zap.String("subtrace_id", subtraceID),
			zap.Int("span_count", len(state.Spans)))
	}

	// Build output traces
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	state.Resource.Resource().CopyTo(rs.Resource())

	ss := rs.ScopeSpans().AppendEmpty()
	state.Scope.Scope().CopyTo(ss.Scope())

	for _, span := range state.Spans {
		span.CopyTo(ss.Spans().AppendEmpty())
	}

	// Send to next consumer
	return p.nextConsumer.ConsumeTraces(ctx, td)
}
