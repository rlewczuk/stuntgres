package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func aggregateCombineActiveCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, operation func(context.Context) fixture.RelationOutcome) {
	t.Helper()
	if b.PG {
		fixture.SleepActiveCancelWithContext(t, p, b, c, query, "SQL function \"ac_sleep_step\" statement 1", nil, operation)
		return
	}
	// The fixture owns cancellation and bounded join. Its launcher predicate
	// observes the real coordinator; the admitted callback then requires a real
	// partial worker inside the SQL support body's sleep before cancellation.
	fixture.BinaryConversionActiveCancelAfter(t, p, b, c, query, "FinalizeParallelAggregate", func(ctx context.Context) {
		stacks := make([]byte, 64<<10)
		timer := time.NewTimer(time.Hour)
		if !timer.Stop() {
			<-timer.C
		}
		defer timer.Stop()
		for {
			if err := ctx.Err(); err != nil {
				t.Fatal("aggregate worker sleep admission", err)
			}
			n := runtime.Stack(stacks, true)
			for n == len(stacks) {
				stacks = make([]byte, len(stacks)*2)
				n = runtime.Stack(stacks, true)
			}
			for _, frames := range bytes.Split(stacks[:n], []byte("\n\n")) {
				if created := bytes.Index(frames, []byte("\ncreated by ")); created >= 0 {
					frames = frames[:created]
				}
				if bytes.Contains(frames, []byte("datetimeSleepWaitRemaining")) && bytes.Contains(frames, []byte("InvokeAggregateSQLRoutine")) && bytes.Contains(frames, []byte("runAggregatePartition")) {
					return
				}
			}
			timer.Reset(time.Millisecond)
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		}
	}, operation)
}
