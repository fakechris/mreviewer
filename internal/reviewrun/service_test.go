package reviewrun

import (
	"context"
	"errors"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/hooks"
	"github.com/mreviewer/mreviewer/internal/scheduler"
)

type fakeEventProcessor struct {
	ev         hooks.NormalizedEvent
	hookEventID int64
	calls      int
	err        error
}

func (f *fakeEventProcessor) ProcessEventWithQuerier(_ context.Context, _ db.Querier, ev hooks.NormalizedEvent, hookEventID int64) error {
	f.calls++
	f.ev = ev
	f.hookEventID = hookEventID
	return f.err
}

type fakeRunProcessor struct {
	run    db.ReviewRun
	calls  int
	outcome scheduler.ProcessOutcome
	err    error
}

func (f *fakeRunProcessor) ProcessRun(_ context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error) {
	f.calls++
	f.run = run
	return f.outcome, f.err
}

func TestServiceProcessEventWithQuerierDelegates(t *testing.T) {
	eventProcessor := &fakeEventProcessor{}
	svc := NewService(eventProcessor, nil)
	ev := hooks.NormalizedEvent{ProjectID: 42, MRIID: 7, Action: "open"}

	if err := svc.ProcessEventWithQuerier(context.Background(), nil, ev, 99); err != nil {
		t.Fatalf("ProcessEventWithQuerier: %v", err)
	}
	if eventProcessor.calls != 1 {
		t.Fatalf("eventProcessor calls = %d, want 1", eventProcessor.calls)
	}
	if eventProcessor.ev.ProjectID != 42 || eventProcessor.hookEventID != 99 {
		t.Fatalf("delegated event = %+v hookEventID=%d", eventProcessor.ev, eventProcessor.hookEventID)
	}
}

func TestServiceProcessRunDelegates(t *testing.T) {
	runProcessor := &fakeRunProcessor{
		outcome: scheduler.ProcessOutcome{Status: "requested_changes"},
	}
	svc := NewService(nil, runProcessor)
	run := db.ReviewRun{ID: 123}

	outcome, err := svc.ProcessRun(context.Background(), run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if runProcessor.calls != 1 {
		t.Fatalf("runProcessor calls = %d, want 1", runProcessor.calls)
	}
	if runProcessor.run.ID != 123 {
		t.Fatalf("delegated run ID = %d, want 123", runProcessor.run.ID)
	}
	if outcome.Status != "requested_changes" {
		t.Fatalf("outcome.Status = %q, want requested_changes", outcome.Status)
	}
}

func TestServiceRequiresConfiguredDelegates(t *testing.T) {
	svc := NewService(nil, nil)

	if err := svc.ProcessEventWithQuerier(context.Background(), nil, hooks.NormalizedEvent{}, 0); err == nil {
		t.Fatal("ProcessEventWithQuerier error = nil, want non-nil")
	}

	if _, err := svc.ProcessRun(context.Background(), db.ReviewRun{}); err == nil {
		t.Fatal("ProcessRun error = nil, want non-nil")
	}
}

func TestServicePropagatesDelegateErrors(t *testing.T) {
	wantEventErr := errors.New("event failed")
	wantRunErr := errors.New("run failed")
	svc := NewService(
		&fakeEventProcessor{err: wantEventErr},
		&fakeRunProcessor{err: wantRunErr},
	)

	if err := svc.ProcessEventWithQuerier(context.Background(), nil, hooks.NormalizedEvent{}, 0); !errors.Is(err, wantEventErr) {
		t.Fatalf("ProcessEventWithQuerier error = %v, want %v", err, wantEventErr)
	}
	if _, err := svc.ProcessRun(context.Background(), db.ReviewRun{}); !errors.Is(err, wantRunErr) {
		t.Fatalf("ProcessRun error = %v, want %v", err, wantRunErr)
	}
}
