package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReferenceSettingsPublicTimeoutStages(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, stage := range []string{"direct", "prepare", "prepared-execute"} {
			t.Run(backend.name+"/"+stage, func(t *testing.T) { settingsTimeoutStage(t, p, backend, stage, false) })
		}
	}
}

func TestReferenceSettingsPublicTimeoutRollbackCleanup(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, stage := range []string{"direct", "prepare", "prepared-execute"} {
			t.Run(backend.name+"/"+stage, func(t *testing.T) { settingsTimeoutStage(t, p, backend, stage, true) })
		}
	}
}

// One stage owns its physical connection from baseline through closure. Workloads
// end in NULL, so a fast execution fails atomically instead of requiring a retry.
func settingsTimeoutStage(t *testing.T, p *referencePair, backend referenceBackend, stage string, rollback bool) {
	t.Helper()
	owner, err := backend.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	defer func() {
		if !rollback {
			_ = owner.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	peer, err := backend.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	name := "sentinel"
	run := func(e referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, e, backend.recorder, s) }
	run(owner, localReferenceSelect(backend.pg, "SHOW statement_timeout", "statement_timeout", "0"))
	run(peer, localReferenceSelect(backend.pg, "SHOW statement_timeout", "statement_timeout", "0"))
	run(owner, referenceExec("CREATE TABLE "+name+"(v INT NOT NULL)", 0))
	run(owner, referenceExec("INSERT INTO "+name+" VALUES(7)", 1))
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := peer.ExecContext(cleanup, "DROP TABLE "+name); err != nil {
			t.Error("dropping timeout fixture", err)
		}
	}()
	identity := settingsPhysical(t, owner)
	retained, err := owner.QueryContext(p.ctx, "SELECT v FROM "+name)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	checkRetained := settingsTimeoutCursor(t, retained, backend.pg)
	sizes := []int{16384, 65536, 262144, 1048576}
	queries := make([]string, len(sizes))
	held := make([]*sql.Stmt, len(sizes))
	for i, size := range sizes {
		queries[i] = "INSERT INTO " + name + " VALUES " + strings.Repeat("(8),", size) + "(NULL)"
		if stage == "prepared-execute" {
			held[i], err = owner.PrepareContext(p.ctx, queries[i])
			if err != nil {
				t.Fatal("prepare at timeout0", err)
			}
			defer held[i].Close()
		}
	}
	// Once armed, the next owner SQL must be the intended workload: even SHOW
	// can legitimately exceed 1ms under scheduling pressure. Setting encoding is
	// independently asserted by TestReferenceSettingsSQL; cleanup is checked at 0.
	if !rollback {
		run(owner, referenceExec("SET statement_timeout TO '1ms'", 0))
	}
	timedOut := false
	for i, query := range queries {
		var tx *sql.Tx
		var executor referenceExecutor = owner
		var statement *sql.Stmt
		if rollback {
			tx, err = owner.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			executor = tx
			run(tx, referenceExec("SET SESSION statement_timeout TO '1ms'", 0))
		}
		if contextErr := p.ctx.Err(); contextErr != nil {
			t.Fatal("fixture context expired before server timeout workload", contextErr)
		}
		switch stage {
		case "direct":
			_, err = executor.ExecContext(p.ctx, query)
		case "prepare":
			statement, err = executor.PrepareContext(p.ctx, query)
		case "prepared-execute":
			// Execute the actual Conn-held statement on this same physical transaction
			// owner; Tx.Stmt could substitute a reprepare and lose the captured handle.
			_, err = held[i].ExecContext(p.ctx)
		}
		if statement != nil {
			statement.Close()
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) && state.SQLState() == "57014" {
			if contextErr := p.ctx.Err(); contextErr != nil {
				t.Fatal("57014 coincided with Go-context cancellation", contextErr)
			}
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to statement timeout"}
			// REL_18_0 simple-query parsing can consume the timer before the second
			// start_xact_command rearms it and clears its indicator. QueryCancelPending
			// survives, so ProcessInterrupts reports user request for this server timer.
			if backend.pg && stage == "direct" {
				want.Message = "canceling statement due to user request"
			}
			referenceAssertDiagnostic(t, err, &want)
			timedOut = true
		} else if stage == "prepare" {
			if err != nil {
				t.Fatal("Prepare stage", err)
			}
		} else if !errors.As(err, &state) || state.SQLState() != "23502" {
			t.Fatal(stage, err)
		}
		if rollback {
			if err := tx.Rollback(); err != nil {
				t.Fatal("public rollback cleanup", err)
			}
		}
		run(peer, referenceSelect("SELECT v FROM "+name, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if rollback {
			run(owner, referenceSelect("SELECT v FROM "+name, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(owner, localReferenceSelect(backend.pg, "SHOW statement_timeout", "statement_timeout", "0"))
		}
		if settingsPhysical(t, owner) != identity {
			t.Fatal("timeout replaced physical owner")
		}
		if timedOut {
			break
		}
	}
	if !timedOut {
		t.Fatal("bounded actual command workload did not time out")
	}
	run(peer, localReferenceSelect(backend.pg, "SHOW statement_timeout", "statement_timeout", "0"))
	if !backend.pg {
		checkRetained()
	}
	if rollback {
		settingsTimeoutRecovery(t, p, backend, owner, name, identity)
	}
	// The implicit owner deliberately remains armed until physical disposal.
}

func settingsTimeoutCursor(t *testing.T, retained *sql.Rows, postgres bool) func() {
	t.Helper()
	settingsIntegralMetadata(t, retained, postgres)
	checkRetained := func() {
		if !retained.Next() {
			t.Fatal("retained cursor missing", retained.Err())
		}
		var value int64
		if err := retained.Scan(&value); err != nil || value != 7 {
			t.Fatal(value, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained cardinality", retained.Err())
		}
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// pq owns a streaming wire result; drain before reusing that transport. Native
	// owns a detached snapshot and keeps the actual cursor across the timed work.
	if postgres {
		checkRetained()
	}
	return checkRetained
}

// Recovery uses the same physical owner and an actual held SHOW statement.
// Native precancellation is synchronous; pq needs an admitted server cancellation
// to distinguish genuine cancellation from a fast command winning a context race.
func settingsTimeoutRecovery(t *testing.T, p *referencePair, backend referenceBackend, owner *sql.Conn, name string, identity any) {
	t.Helper()
	const query = "SHOW statement_timeout"
	held, err := owner.PrepareContext(p.ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	heldOwner := referenceHeldStatement{referenceExecutor: owner, statement: held, query: query}
	if backend.pg {
		for _, prepared := range []bool{false, true} {
			referenceSessionCancel(t, p.ctx, owner, prepared)
			referenceRunStep(t, p.ctx, heldOwner, backend.recorder, localReferenceSelect(true, query, "statement_timeout", "0"))
			referenceRunStep(t, p.ctx, owner, backend.recorder, referenceSelect("SELECT v FROM "+name, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if settingsPhysical(t, owner) != identity {
				t.Fatal("admitted cancellation replaced rollback owner")
			}
		}
	} else {
		canceled, cancel := context.WithCancel(p.ctx)
		cancel()
		if result, execErr := owner.ExecContext(canceled, "SHOW application_name"); result != nil || !errors.Is(execErr, context.Canceled) {
			t.Fatal(result, execErr)
		}
		expired, cancelDeadline := context.WithDeadline(p.ctx, time.Unix(0, 0))
		defer cancelDeadline()
		if result, execErr := owner.ExecContext(expired, "SHOW application_name"); result != nil || !errors.Is(execErr, context.DeadlineExceeded) {
			t.Fatal(result, execErr)
		}
		referenceRunStep(t, p.ctx, heldOwner, backend.recorder, localReferenceSelect(false, query, "statement_timeout", "0"))
		referenceRunStep(t, p.ctx, owner, backend.recorder, referenceSelect("SELECT v FROM "+name, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if settingsPhysical(t, owner) != identity {
			t.Fatal("precancellation replaced rollback owner")
		}
	}
}
