package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

func TestWaitFor_TransientErrorsAreRetried(t *testing.T) {
	n := 0
	err := WaitFor(context.Background(), time.Millisecond, func(context.Context) (bool, string, error) {
		n++
		if n <= MaxConsecutiveTransientErrors {
			return false, "", &APIError{StatusCode: http.StatusServiceUnavailable}
		}
		return true, "", nil
	})
	if err != nil || n != MaxConsecutiveTransientErrors+1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestWaitFor_TooManyTransientErrorsFail(t *testing.T) {
	n := 0
	err := WaitFor(context.Background(), time.Millisecond, func(context.Context) (bool, string, error) {
		n++
		return false, "", &APIError{StatusCode: http.StatusBadGateway, Body: fmt.Sprint("attempt ", n)}
	})
	if n != MaxConsecutiveTransientErrors+1 || err == nil || !strings.Contains(err.Error(), fmt.Sprint("attempt ", n)) {
		t.Fatalf("want the last of %d transient errors, got err=%v after n=%d", MaxConsecutiveTransientErrors+1, err, n)
	}
}

func TestWaitFor_TransientCounterResets(t *testing.T) {
	n := 0
	err := WaitFor(context.Background(), time.Millisecond, func(context.Context) (bool, string, error) {
		n++
		switch {
		case n == 20:
			return true, "", nil
		case n%5 == 0: // a success after every 4 errors resets the counter
			return false, "building", nil
		}
		return false, "", &APIError{StatusCode: http.StatusServiceUnavailable}
	})
	if err != nil || n != 20 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestWaitFor_TransientErrorIsTimeoutStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	n := 0
	err := WaitFor(ctx, 10*time.Millisecond, func(context.Context) (bool, string, error) {
		n++
		if n == 1 {
			return false, "building", nil
		}
		return false, "", &APIError{StatusCode: http.StatusServiceUnavailable, Method: "GET", URL: "u"}
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "transient error") {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitFor_FatalAPIErrorStops(t *testing.T) {
	n := 0
	err := WaitFor(context.Background(), time.Millisecond, func(context.Context) (bool, string, error) {
		n++
		return false, "", &APIError{StatusCode: http.StatusForbidden}
	})
	if err == nil || n != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}
