package client

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWaitFor_DoneAfterN(t *testing.T) {
	n := 0
	err := WaitFor(context.Background(), time.Millisecond, func(context.Context) (bool, string, error) {
		n++
		return n == 3, "x", nil
	})
	if err != nil || n != 3 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestWaitFor_ErrorStops(t *testing.T) {
	boom := errors.New("boom")
	n := 0
	err := WaitFor(context.Background(), time.Millisecond, func(context.Context) (bool, string, error) {
		n++
		return false, "", boom
	})
	if !errors.Is(err, boom) || n != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestWaitFor_TimeoutReportsLastStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := WaitFor(ctx, 10*time.Millisecond, func(context.Context) (bool, string, error) {
		return false, "building", nil
	})
	if err == nil || !strings.Contains(err.Error(), "building") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}
