package trace

import (
	"context"
	"sync"
	"sync/atomic"
)

type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Name         string
	Attributes   map[string]string
}

type Recorder struct {
	mu       sync.Mutex
	spans    []Span
	traceSeq atomic.Uint64
	spanSeq  atomic.Uint64
}

type spanContextKey struct{}

type spanContext struct {
	traceID string
	spanID  string
}

func NewRecorder() *Recorder { return &Recorder{} }

func (r *Recorder) Start(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	if r == nil {
		return ctx, func() {}
	}
	parent, _ := ctx.Value(spanContextKey{}).(spanContext)
	traceID := parent.traceID
	if traceID == "" {
		traceID = r.nextTraceID()
	}
	spanID := r.nextSpanID()
	span := Span{TraceID: traceID, SpanID: spanID, ParentSpanID: parent.spanID, Name: name, Attributes: cloneAttrs(attrs)}
	r.mu.Lock()
	r.spans = append(r.spans, span)
	r.mu.Unlock()
	ctx = context.WithValue(ctx, spanContextKey{}, spanContext{traceID: traceID, spanID: spanID})
	return ctx, func() {}
}

func (r *Recorder) Spans() []Span {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cloned := make([]Span, len(r.spans))
	copy(cloned, r.spans)
	return cloned
}

func CurrentTraceID(ctx context.Context) string {
	span, _ := ctx.Value(spanContextKey{}).(spanContext)
	return span.traceID
}

func (r *Recorder) nextTraceID() string {
	return encodeID(r.traceSeq.Add(1))
}

func (r *Recorder) nextSpanID() string {
	return encodeID(r.spanSeq.Add(1))
}

func encodeID(v uint64) string {
	const hex = "0123456789abcdef"
	buf := make([]byte, 16)
	for i := len(buf) - 1; i >= 0; i-- {
		buf[i] = hex[v&0xf]
		v >>= 4
	}
	return string(buf)
}

func cloneAttrs(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(attrs))
	for k, v := range attrs {
		cloned[k] = v
	}
	return cloned
}
