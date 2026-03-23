package timeutil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSleepContextReturnsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := SleepContext(ctx, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SleepContext error = %v, want context.Canceled", err)
	}
}
