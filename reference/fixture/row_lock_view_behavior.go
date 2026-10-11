package fixture

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Native cancellation is its own public control, not proof of a PostgreSQL queue.
// The PostgreSQL branch below instead observes both backend PIDs and the real wait.
func ViewNativeBlocked(t *testing.T, ctx context.Context, x Executor, q string, prepared bool) {
	t.Helper()
	bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	out := RowExecute(bounded, x, q, prepared)
	if !errors.Is(out.Err, context.DeadlineExceeded) || out.Result != nil {
		t.Fatal("native view conflict", out)
	}
	var state interface{ SQLState() string }
	if errors.As(out.Err, &state) {
		t.Fatal("caller deadline fabricated SQLSTATE", out.Err)
	}
}
