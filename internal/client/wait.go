package client

import (
	"context"
	"fmt"
	"time"
)

// MaxConsecutiveTransientErrors is how many transient errors in a row WaitFor
// tolerates; the next one fails the wait.
const MaxConsecutiveTransientErrors = 5

// WaitFor calls check immediately and then every interval until it reports
// done, returns an error, or ctx ends. On ctx expiry the last observed status
// is included in the error.
//
// A transient check error (see IsTransient) does not end the wait: it becomes
// the observed status ("transient error: ...") and polling continues. More
// than MaxConsecutiveTransientErrors of them in a row fail the wait with the
// last one. Any other error ends the wait at once.
func WaitFor(ctx context.Context, interval time.Duration, check func(ctx context.Context) (done bool, status string, err error)) error {
	var status string
	transient := 0
	for {
		done, s, err := check(ctx)
		switch {
		case err != nil && IsTransient(err):
			transient++
			if transient > MaxConsecutiveTransientErrors {
				return fmt.Errorf("giving up after %d consecutive transient errors: %w", transient, err)
			}
			status = "transient error: " + err.Error()
		case err != nil:
			return err
		case done:
			return nil
		default:
			transient = 0
			status = s
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting; last observed status %q: %w", status, ctx.Err())
		case <-time.After(interval):
		}
	}
}
