package reviewstatus

import "testing"

func TestStageDescriptionCompletedAndUnknown(t *testing.T) {
	if got := StageCompleted.Description(); got != "AI review is complete" {
		t.Fatalf("StageCompleted.Description() = %q, want %q", got, "AI review is complete")
	}
	if got := Stage("mystery").Description(); got != "Unknown review stage" {
		t.Fatalf("unknown stage description = %q, want %q", got, "Unknown review stage")
	}
}
