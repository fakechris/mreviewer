package llm

import (
	"context"
	"testing"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
)

func TestUnwrapReviewEnvelope(t *testing.T) {
	inner := `{"schema_version":"1.0","review_run_id":"0","summary":"s","findings":[]}`
	if got := unwrapReviewEnvelope(`{"review": ` + inner + `}`); got != inner {
		t.Fatalf("unwrap = %q", got)
	}
	if got := unwrapReviewEnvelope(inner); got != inner {
		t.Fatalf("direct result must be untouched, got %q", got)
	}
	twoKeys := `{"review": ` + inner + `, "extra": 1}`
	if got := unwrapReviewEnvelope(twoKeys); got != twoKeys {
		t.Fatalf("multi-key object must be untouched, got %q", got)
	}
	if got := unwrapReviewEnvelope(`{"note": {"text": "x"}}`); got != `{"note": {"text": "x"}}` {
		t.Fatalf("non-review inner object must be untouched, got %q", got)
	}
	if got := unwrapReviewEnvelope("not json"); got != "not json" {
		t.Fatalf("non-json must be untouched, got %q", got)
	}
}

func TestSchemaHarnessAcceptsWrappedReviewResult(t *testing.T) {
	inner := `{"schema_version":"1.0","review_run_id":"0","summary":"ok","findings":[]}`
	res, err := (ReviewSchemaHarness{}).Execute(context.Background(), ctxpkg.ReviewRequest{}, StructuredOutputCandidate{RawText: `{"review": ` + inner + `}`}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Result.Summary != "ok" || !res.Report.FinalValid {
		t.Fatalf("result = %+v report = %+v", res.Result, res.Report)
	}
}
