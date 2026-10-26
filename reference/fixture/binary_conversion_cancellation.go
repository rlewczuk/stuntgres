package fixture

import (
	"bytes"
	"context"
	"database/sql"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Observe real provider/server admission before cancellation. Admission has a
// deadline and cleanup has a bounded worker join on every exit, before the
// caller closes its held statements or physical connection.
func BinaryConversionActiveCancel(t *testing.T, p *Pair, b Backend, c *sql.Conn, query, provider string, operation func(context.Context) RelationOutcome) {
	t.Helper()
	activeProviderCancel(t, p, b, c, query, provider, "", "", nil, func(ctx context.Context) RelationOutcome {
		if !b.PG {
			ctx = &nativeProviderPollContext{Context: ctx, provider: []byte(provider), worker: []byte("activeProviderCancel")}
		}
		return operation(ctx)
	})
}

// nativeProviderPollContext holds a real cancellation poll on the operation's
// provider stack until the observer cancels the underlying context. Finite work
// cannot run between samples and escape admission. Other callers of Err remain
// ordinary context observers; no cancellation error is manufactured here.
// Progress-callback and sleep helpers deliberately retain their existing routes.
type nativeProviderPollContext struct {
	context.Context
	provider, worker []byte
	mu               sync.Mutex
	stacks           []byte
}

func (c *nativeProviderPollContext) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if len(c.stacks) == 0 {
		c.stacks = make([]byte, 16<<10)
	}
	n := runtime.Stack(c.stacks, false)
	for n == len(c.stacks) {
		c.stacks = make([]byte, len(c.stacks)*2)
		n = runtime.Stack(c.stacks, false)
	}
	frames := c.stacks[:n]
	if created := bytes.Index(frames, []byte("\ncreated by ")); created >= 0 {
		frames = frames[:created]
	}
	admitted := bytes.Contains(frames, c.provider) && bytes.Contains(frames, c.worker)
	c.mu.Unlock()
	if admitted {
		<-c.Context.Done()
	}
	return c.Context.Err()
}

// BinaryConversionActiveCancelAfter additionally observes caller-defined progress
// after provider/server admission and before cancellation, under the same bounded
// context and worker cleanup. The callback must not use the busy connection.
func BinaryConversionActiveCancelAfter(t *testing.T, p *Pair, b Backend, c *sql.Conn, query, provider string, admitted func(context.Context), operation func(context.Context) RelationOutcome) {
	t.Helper()
	activeProviderCancel(t, p, b, c, query, provider, "", "", admitted, operation)
}

// SleepActiveCancel observes a real wait, rather than a precancelled request.
func SleepActiveCancel(t *testing.T, p *Pair, b Backend, c *sql.Conn, query string, admitted func(context.Context), operation func(context.Context) RelationOutcome) {
	t.Helper()
	activeProviderCancel(t, p, b, c, query, "datetimeSleepWaitRemaining", "PgSleep", "", admitted, operation)
}

// SleepActiveCancelWithContext preserves the same admission and joined cleanup,
// while checking a caller's independently observed PostgreSQL error provenance.
func SleepActiveCancelWithContext(t *testing.T, p *Pair, b Backend, c *sql.Conn, query, postgresContext string, admitted func(context.Context), operation func(context.Context) RelationOutcome) {
	t.Helper()
	activeProviderCancel(t, p, b, c, query, "datetimeSleepWaitRemaining", "PgSleep", postgresContext, admitted, operation)
}

func activeProviderCancel(t *testing.T, p *Pair, b Backend, c *sql.Conn, query, provider, waitEvent, postgresContext string, admitted func(context.Context), operation func(context.Context) RelationOutcome) {
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
			observed, stacks = nativeOperationStack(stacks, providerSymbol, workerSymbol)
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
		AssertDiagnostic(t, outcome.Err, &Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: postgresContext})
	} else {
		LexicalOrdinary(t, outcome.Err, context.Canceled)
	}
}

// nativeOperationStack matches only live frames from one complete goroutine,
// never creation metadata or frames belonging to an unrelated worker.
func nativeOperationStack(stacks []byte, symbols ...[]byte) (bool, []byte) {
	stacks = stacks[:cap(stacks)]
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
		if created := bytes.Index(frames, []byte("\ncreated by ")); created >= 0 {
			frames = frames[:created]
		}
		matches := true
		for _, symbol := range symbols {
			if !bytes.Contains(frames, symbol) {
				matches = false
				break
			}
		}
		if matches {
			return true, stacks[:n]
		}
		if end == len(remaining) {
			break
		}
		remaining = remaining[end+2:]
	}
	return false, stacks[:n]
}

// NativeUpdateRowWait proves the actual LockOperation worker is parked in the
// mutation's row-request wait before its holder commits. The unified lock owner
// names this parked frame relationRequest.wait, including row resources.
func NativeUpdateRowWait(t *testing.T, ctx context.Context, completed <-chan struct{}) {
	t.Helper()
	nativeMutationRowWait(t, ctx, completed, "(*StatementScope).acquireUpdateRowMode")
}

// NativeDeleteRowWait observes the actual LockOperation delete worker parked on
// a row request before its holder releases the conflicting transaction.
func NativeDeleteRowWait(t *testing.T, ctx context.Context, completed <-chan struct{}) {
	t.Helper()
	nativeMutationRowWait(t, ctx, completed, "(*StatementScope).acquireRow")
}

// NativeUniqueKeyWait observes the LockOperation worker itself parked in the
// unique-key claim, not another request or a previously completed timeout probe.
func NativeUniqueKeyWait(t *testing.T, ctx context.Context, completed <-chan struct{}) {
	t.Helper()
	nativeMutationRowWait(t, ctx, completed, "(*statementHost).ClaimUniqueKey")
}

func nativeMutationRowWait(t *testing.T, ctx context.Context, completed <-chan struct{}, owner string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stacks := make([]byte, 64<<10)
	symbols := [][]byte{[]byte("(*relationRequest).wait"), []byte(owner), []byte("fixture.LockOperation.func")}
	var poll *time.Timer
	defer func() {
		if poll != nil {
			poll.Stop()
		}
	}()
	for {
		select {
		case <-completed:
			t.Fatal("native mutation completed before row-wait admission")
		case <-ctx.Done():
			t.Fatalf("native mutation row-wait admission: %v\n%s", ctx.Err(), stacks)
		default:
		}
		var observed bool
		observed, stacks = nativeOperationStack(stacks, symbols...)
		if observed {
			return
		}
		// Preserve the existing observer's scheduling courtesy after a
		// stop-the-world sample; elapsed time never establishes admission.
		if poll == nil {
			poll = time.NewTimer(time.Millisecond)
		} else {
			poll.Reset(time.Millisecond)
		}
		select {
		case <-completed:
			t.Fatal("native mutation completed before row-wait admission")
		case <-ctx.Done():
			t.Fatalf("native mutation row-wait admission: %v\n%s", ctx.Err(), stacks)
		case <-poll.C:
		}
	}
}
