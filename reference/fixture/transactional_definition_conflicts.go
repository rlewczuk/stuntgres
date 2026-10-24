package fixture

import (
	"context"
	"database/sql"
	"regexp"
	"runtime"
	"testing"
	"time"
)

// DefinitionLockOperation starts a joined worker and observes the independently
// specified catalog relation, transaction or object queue before returning.
// Native admission uses the existing real scheduler observer, not pg_locks emulation.
func DefinitionLockOperation(t *testing.T, ctx context.Context, p *Pair, b Backend, c, holder *sql.Conn, query, kind, mode string, class, object int64) func() RelationOutcome {
	t.Helper()
	return DefinitionLockCall(t, ctx, p, b, c, holder, query, kind, mode, class, object, func(worker context.Context) RelationOutcome {
		return RowExecute(worker, c, query, false)
	})
}

// DefinitionLockCall observes the same exact queue for genuinely held execution.
func DefinitionLockCall(t *testing.T, ctx context.Context, p *Pair, b Backend, c, holder *sql.Conn, query, kind, mode string, class, object int64, call func(context.Context) RelationOutcome) func() RelationOutcome {
	t.Helper()
	waiterPID, holderPID := 0, 0
	if b.PG {
		waiterPID, holderPID = RowPIDs(t, ctx, c, holder)
	}
	finished := make(chan struct{})
	join := LockOperation(t, ctx, func(worker context.Context) RelationOutcome {
		defer close(finished)
		return call(worker)
	})
	if !b.PG {
		RelationLockPending(t, p, b, 0, 0, 0, mode, finished)
		return join
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		select {
		case <-finished:
			t.Fatal("definition command completed before required queue", query)
		default:
		}
		var pending bool
		err := Admin.QueryRowContext(bounded, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype=$3 AND mode=$4 AND NOT granted AND ($3<>'relation' OR relation=$6) AND ($3<>'object' OR (classid=$5 AND objid=$6 AND objsubid=0))) AND $2=ANY(pg_blocking_pids($1))`, waiterPID, holderPID, kind, mode, class, object).Scan(&pending)
		if err != nil {
			t.Fatal("definition queue", err)
		}
		if pending {
			return join
		}
		runtime.Gosched()
	}
}

// DefinitionIndexWaitDiagnostic keeps every logical field exact. Only PostgreSQL
// physical heap coordinates vary; native cannot claim a heap tuple Context.
func DefinitionIndexWaitDiagnostic(t *testing.T, err error, code, message, index string, postgres bool) {
	t.Helper()
	want := ControlsReferenceDiagnostic(code, message)
	if postgres && index != "" {
		got := ExtractDiagnostic(t, err)
		pattern := regexp.MustCompile(`^while inserting index tuple \([0-9]+,[1-9][0-9]*\) in relation "` + regexp.QuoteMeta(index) + `"$`)
		if !pattern.MatchString(got.Context) {
			t.Fatalf("definition index tuple Context got %q", got.Context)
		}
		want.Context = got.Context
	}
	AssertDiagnostic(t, err, &want)
}

// DefinitionActiveCancel observes the exact queue, permits independent peer
// work, then cancels and joins while preserving the physical SQL owner.
func DefinitionActiveCancel(t *testing.T, p *Pair, b Backend, c, holder *sql.Conn, query, kind, mode string, class, object int64, index string, admitted func(context.Context)) {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	pid := 0
	if b.PG {
		pid = LockPID(t, ctx, c)
	}
	join := DefinitionLockOperation(t, ctx, p, b, c, holder, query, kind, mode, class, object)
	if admitted != nil {
		admitted(ctx)
	}
	if b.PG {
		var signaled bool
		if err := Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
			t.Fatal("definition cancellation", signaled, err)
		}
	} else {
		cancel()
	}
	outcome := join()
	if outcome.Rows != nil {
		outcome.Rows.Close()
		t.Fatal("canceled definition returned rows")
	}
	if outcome.Result != nil {
		t.Fatal("canceled definition returned result")
	}
	if b.PG {
		DefinitionIndexWaitDiagnostic(t, outcome.Err, "57014", "canceling statement due to user request", index, true)
	} else {
		LexicalOrdinary(t, outcome.Err, context.Canceled)
	}
}
