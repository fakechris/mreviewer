package trace

import (
	"context"
	"testing"
)

func TestSingleTraceAcrossPipeline(t *testing.T) {
	recorder := NewRecorder()
	ctx, end := recorder.Start(nilContext(), "webhook.verify", nil)
	defer end()
	names := []string{"gitlab.fetch_mr", "gitlab.fetch_versions", "gitlab.fetch_diffs", "rules.load", "llm.request", "parser.validate", "dedupe.match", "gitlab.create_discussion"}
	for _, name := range names {
		var finish func()
		ctx, finish = recorder.Start(ctx, name, nil)
		finish()
	}
	spans := recorder.Spans()
	if len(spans) != len(names)+1 {
		t.Fatalf("span count = %d, want %d", len(spans), len(names)+1)
	}
	traceID := spans[0].TraceID
	for _, span := range spans {
		if span.TraceID != traceID {
			t.Fatalf("span %s trace_id = %q, want %q", span.Name, span.TraceID, traceID)
		}
	}
}

func nilContext() context.Context { return context.Background() }
