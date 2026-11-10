package client

import (
	"context"
	"fmt"
	"time"
)

// WaitFor calls check immediately and then every interval until it reports
// done, returns an error, or ctx ends. On ctx expiry the last observed status
// is included in the error.
func WaitFor(ctx context.Context, interval time.Duration, check func(ctx context.Context) (done bool, status string, err error)) error {
	var status string
	for {
		done, s, err := check(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		status = s
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting; last observed status %q: %w", status, ctx.Err())
		case <-time.After(interval):
		}
	}
}
