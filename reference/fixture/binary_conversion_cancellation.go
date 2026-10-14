package fixture

import (
	"bytes"
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"
)

// Observe real provider/server admission before cancellation. Admission has a
// deadline and cleanup has a bounded worker join on every exit, before the
// caller closes its held statements or physical connection.
func BinaryConversionActiveCancel(t *testing.T, p *Pair, b Backend, c *sql.Conn, query, provider string, operation func(context.Context) RelationOutcome) {
	t.Helper()
	activeProviderCancel(t, p, b, c, query, provider, "", nil, operation)
}

// SleepActiveCancel observes a real wait, rather than a precancelled request.
func SleepActiveCancel(t *testing.T, p *Pair, b Backend, c *sql.Conn, query string, admitted func(context.Context), operation func(context.Context) RelationOutcome) {
	t.Helper()
	activeProviderCancel(t, p, b, c, query, "datetimeSleepWaitRemaining", "PgSleep", admitted, operation)
}

func activeProviderCancel(t *testing.T, p *Pair, b Backend, c *sql.Conn, query, provider, waitEvent string, admitted func(context.Context), operation func(context.Context) RelationOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
	defer cancel()
	pid := 0
	if b.PG {
		pid = LockPID(t, ctx, c)
	}
	done := make(chan RelationOutcome, 1)
	joined := false
	defer func() {
		cancel()
		if !joined {
			outcome := LockResult(t, done)
			if outcome.Rows != nil {
				outcome.Rows.Close()
			}
		}
	}()
	go func() { done <- operation(ctx) }()
	// GoroutineProfile truncates to 32 PCs. Real SQL executor decomposition can
	// place the launcher below that frontier; retain both exact frame predicates
	// with complete live stacks rather than admitting a different worker.
	var stacks, providerSymbol, workerSymbol []byte
	if !b.PG {
		stacks = make([]byte, 64<<10)
		providerSymbol, workerSymbol = []byte(provider), []byte("activeProviderCancel")
	}
	var poll *time.Timer
	defer func() {
		if poll != nil {
			poll.Stop()
		}
	}()
	observed := false
	for !observed && ctx.Err() == nil {
		select {
		case outcome := <-done:
			joined = true
			if outcome.Rows != nil {
				outcome.Rows.Close()
			}
			t.Fatal("conversion completed before active admission", query, outcome.Err)
		default:
		}
		if b.PG {
			if err := Admin.QueryRowContext(ctx, "SELECT COALESCE((SELECT state='active' AND query=$2 AND ($3='' OR wait_event=$3) FROM pg_stat_activity WHERE pid=$1),false)", pid, query, waitEvent).Scan(&observed); err != nil {
				t.Fatal("conversion admission", err)
			}
		} else {
			n := runtime.Stack(stacks, true)
			for n == len(stacks) {
				stacks = make([]byte, len(stacks)*2)
				n = runtime.Stack(stacks, true)
			}
			remaining := stacks[:n]
			for len(remaining) != 0 {
				end := bytes.Index(remaining, []byte("\n\n"))
				if end < 0 {
					end = len(remaining)
				}
				frames := remaining[:end]
				// Creation metadata is not a live frame of this goroutine.
				if created := bytes.Index(frames, []byte("\ncreated by ")); created >= 0 {
					frames = frames[:created]
				}
				if bytes.Contains(frames, providerSymbol) && bytes.Contains(frames, workerSymbol) {
					observed = true
					break
				}
				if end == len(remaining) {
					break
				}
				remaining = remaining[end+2:]
			}
			if !observed {
				// Stack(all=true) stops the world. Wait after each unsuccessful
				// complete sample so observation cannot monopolize worker progress.
				// Start the delay here, not on a ticker with accumulated ticks.
				if poll == nil {
					poll = time.NewTimer(time.Millisecond)
				} else {
					poll.Reset(time.Millisecond)
				}
				select {
				case <-ctx.Done():
				case outcome := <-done:
					joined = true
					if outcome.Rows != nil {
						outcome.Rows.Close()
					}
					t.Fatal("conversion completed before active admission", query, outcome.Err)
				case <-poll.C:
				}
			}
		}
	}
	if !observed {
		t.Fatal("conversion admission deadline", query, ctx.Err())
	}
	if admitted != nil {
		admitted(ctx)
	}
	if b.PG {
		var signaled bool
		if err := Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
			t.Fatal("conversion backend cancellation", signaled, err)
		}
	} else {
		cancel()
	}
	outcome := LockResult(t, done)
	joined = true
	if outcome.Rows != nil {
		outcome.Rows.Close()
		t.Fatal("active canceled conversion published cursor")
	}
	if outcome.Result != nil {
		t.Fatal("active canceled conversion published result")
	}
	if b.PG {
		AssertDiagnostic(t, outcome.Err, &Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
	} else {
		LexicalOrdinary(t, outcome.Err, context.Canceled)
	}
}
