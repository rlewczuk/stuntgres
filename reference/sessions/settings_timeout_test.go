package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSettingsPublicTimeoutStages(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, stage := range []string{"direct", "prepare", "prepared-execute"} {
			t.Run(backend.Name+"/"+stage, func(t *testing.T) { settingsTimeoutStage(t, p, backend, stage, false) })
		}
	}
}

func TestReferenceSettingsPublicTimeoutRollbackCleanup(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, stage := range []string{"direct", "prepare", "prepared-execute"} {
			t.Run(backend.Name+"/"+stage, func(t *testing.T) { settingsTimeoutStage(t, p, backend, stage, true) })
		}
	}
}

// Reach larger workloads only when the preceding candidate did not time out.
// Each attempt disposes of its implicit armed owner before acquiring a new one.
func settingsTimeoutStage(t *testing.T, p *fixture.Pair, backend fixture.Backend, stage string, rollback bool) {
	t.Helper()
	for _, size := range []int{16384, 65536, 262144, 1048576} {
		if settingsTimeoutAttempt(t, p, backend, stage, rollback, size) {
			return
		}
	}
	t.Fatal("bounded actual command workload did not time out")
}

// One attempt owns its physical connection from baseline through closure.
// Workloads end in NULL, so a fast execution fails atomically.
func settingsTimeoutAttempt(t *testing.T, p *fixture.Pair, backend fixture.Backend, stage string, rollback bool, size int) bool {
	t.Helper()
	owner, err := backend.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	defer func() {
		if !rollback {
			_ = owner.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	peer, err := backend.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	name := "sentinel"
	run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, backend.Recorder, s) }
	run(owner, localReferenceSelect(backend.PG, "SHOW statement_timeout", "statement_timeout", "0"))
	run(peer, localReferenceSelect(backend.PG, "SHOW statement_timeout", "statement_timeout", "0"))
	run(owner, fixture.Exec("CREATE TABLE "+name+"(v INT NOT NULL)", 0))
	run(owner, fixture.Exec("INSERT INTO "+name+" VALUES(7)", 1))
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := peer.ExecContext(cleanup, "DROP TABLE "+name); err != nil {
			t.Error("dropping timeout fixture", err)
		}
	}()
	identity := settingsPhysical(t, owner)
	retained, err := owner.QueryContext(p.Ctx, "SELECT v FROM "+name)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	checkRetained := settingsTimeoutCursor(t, retained, backend.PG)
	query := settingsTimeoutWorkload(name, size)
	var heldStmt *sql.Stmt
	if stage == "prepared-execute" {
		heldStmt, err = owner.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal("prepare at timeout0", err)
		}
		// Registered before tx.Rollback so fatal paths roll back first.
		defer func() {
			if heldStmt != nil {
				if err := heldStmt.Close(); err != nil {
					t.Error("closing timeout workload", err)
				}
			}
		}()
	}
	// Once armed, the next owner SQL must be the intended workload: even SHOW
	// can legitimately exceed 1ms under scheduling pressure. Setting encoding is
	// independently asserted by TestReferenceSettingsSQL; cleanup is checked at 0.
	if !rollback {
		run(owner, fixture.Exec("SET statement_timeout TO '1ms'", 0))
	}
	var tx *sql.Tx
	var executor fixture.Executor = owner
	var statement *sql.Stmt
	if rollback {
		tx, err = owner.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		executor = tx
		run(tx, fixture.Exec("SET SESSION statement_timeout TO '1ms'", 0))
	}
	if contextErr := p.Ctx.Err(); contextErr != nil {
		t.Fatal("fixture context expired before server timeout workload", contextErr)
	}
	switch stage {
	case "direct":
		_, err = executor.ExecContext(p.Ctx, query)
	case "prepare":
		statement, err = executor.PrepareContext(p.Ctx, query)
	case "prepared-execute":
		// Execute the actual Conn-held statement on this same physical transaction
		// owner; Tx.Stmt could substitute a reprepare and lose the captured handle.
		_, err = heldStmt.ExecContext(p.Ctx)
	}
	if statement != nil {
		statement.Close()
	}
	timedOut := false
	var state interface{ SQLState() string }
	if errors.As(err, &state) && state.SQLState() == "57014" {
		if contextErr := p.Ctx.Err(); contextErr != nil {
			t.Fatal("57014 coincided with Go-context cancellation", contextErr)
		}
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to statement timeout"}
		// REL_18_0 simple-query parsing can consume the timer before the second
		// start_xact_command rearms it and clears its indicator. QueryCancelPending
		// survives, so ProcessInterrupts reports user request for this server timer.
		if backend.PG && stage == "direct" {
			want.Message = "canceling statement due to user request"
		}
		fixture.AssertDiagnostic(t, err, &want)
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
	if heldStmt != nil {
		candidate := heldStmt
		heldStmt = nil
		if err := candidate.Close(); err != nil {
			t.Fatal("closing timeout workload", err)
		}
	}
	run(peer, fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	if rollback {
		run(owner, fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(owner, localReferenceSelect(backend.PG, "SHOW statement_timeout", "statement_timeout", "0"))
	}
	if settingsPhysical(t, owner) != identity {
		t.Fatal("timeout replaced physical owner")
	}
	run(peer, localReferenceSelect(backend.PG, "SHOW statement_timeout", "statement_timeout", "0"))
	if !backend.PG {
		checkRetained()
	}
	if rollback {
		settingsTimeoutRecovery(t, p, backend, owner, name, identity)
	}
	// The implicit owner deliberately remains armed until physical disposal.
	return timedOut
}

// Both timeout families use the same bounded, atomic CPU workload.
func settingsTimeoutWorkload(name string, size int) string {
	return "INSERT INTO " + name + " VALUES " + strings.Repeat("(8),", size) + "(NULL)"
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
func settingsTimeoutRecovery(t *testing.T, p *fixture.Pair, backend fixture.Backend, owner *sql.Conn, name string, identity any) {
	t.Helper()
	const query = "SHOW statement_timeout"
	held, err := owner.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	heldOwner := fixture.HeldStatement{Executor: owner, Statement: held, SQL: query}
	if backend.PG {
		for _, prepared := range []bool{false, true} {
			fixture.SessionCancel(t, p.Ctx, owner, prepared)
			fixture.RunStep(t, p.Ctx, heldOwner, backend.Recorder, localReferenceSelect(true, query, "statement_timeout", "0"))
			fixture.RunStep(t, p.Ctx, owner, backend.Recorder, fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if settingsPhysical(t, owner) != identity {
				t.Fatal("admitted cancellation replaced rollback owner")
			}
		}
	} else {
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if result, execErr := owner.ExecContext(canceled, "SHOW application_name"); result != nil || !errors.Is(execErr, context.Canceled) {
			t.Fatal(result, execErr)
		}
		expired, cancelDeadline := context.WithDeadline(p.Ctx, time.Unix(0, 0))
		defer cancelDeadline()
		if result, execErr := owner.ExecContext(expired, "SHOW application_name"); result != nil || !errors.Is(execErr, context.DeadlineExceeded) {
			t.Fatal(result, execErr)
		}
		fixture.RunStep(t, p.Ctx, heldOwner, backend.Recorder, localReferenceSelect(false, query, "statement_timeout", "0"))
		fixture.RunStep(t, p.Ctx, owner, backend.Recorder, fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if settingsPhysical(t, owner) != identity {
			t.Fatal("precancellation replaced rollback owner")
		}
	}
}
