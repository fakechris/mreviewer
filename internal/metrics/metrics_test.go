package metrics

import "testing"

func TestRunMetrics(t *testing.T) {
	registry := NewRegistry()
	registry.IncCounter("review_run_started_total", map[string]string{"trigger_type": "webhook"})
	registry.IncCounter("review_run_completed_total", map[string]string{"trigger_type": "webhook"})
	registry.ObserveHistogram("provider_latency_ms", nil, 25)
	registry.AddCounter("provider_tokens_total", nil, 77)
	registry.ObserveHistogram("comment_writer_latency_ms", map[string]string{"status": "completed"}, 14)

	if got := registry.CounterValue("review_run_started_total", map[string]string{"trigger_type": "webhook"}); got != 1 {
		t.Fatalf("started counter = %d, want 1", got)
	}
	if got := registry.CounterValue("review_run_completed_total", map[string]string{"trigger_type": "webhook"}); got != 1 {
		t.Fatalf("completed counter = %d, want 1", got)
	}
	if got := registry.CounterValue("provider_tokens_total", nil); got != 77 {
		t.Fatalf("provider tokens = %d, want 77", got)
	}
	if got := registry.HistogramValues("provider_latency_ms", nil); len(got) != 1 || got[0] != 25 {
		t.Fatalf("provider latency samples = %v, want [25]", got)
	}
	if got := registry.HistogramValues("comment_writer_latency_ms", map[string]string{"status": "completed"}); len(got) != 1 || got[0] != 14 {
		t.Fatalf("writer latency samples = %v, want [14]", got)
	}
}
