package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceQueryTableCreateLockingCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
			run(a, fixture.Exec("CREATE TABLE qc_lock_source(i INT)", 0))
			run(a, fixture.Exec("INSERT INTO qc_lock_source VALUES(7)", 1))
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run(tx, fixture.Select("SELECT i FROM qc_lock_source FOR UPDATE", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(c, fixture.Exec("CREATE TABLE qc_lock_nodata AS SELECT i FROM qc_lock_source FOR UPDATE WITH NO DATA", 0))
			run(c, fixture.Select("SELECT i FROM qc_lock_nodata", []string{"i"}, []string{"INT4"}, nil))
			want := fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on row in relation "qc_lock_source"`)
			run(c, fixture.ErrorDiagnostic("CREATE TABLE qc_lock_failed AS SELECT i FROM qc_lock_source FOR UPDATE NOWAIT", want))
			fixture.QueryTableCreateAbsent(t, func(s fixture.Step) { run(c, s) }, "qc_lock_failed")
			tuple := fixture.QueryTableCreateTuple(t, p.Ctx, c, b.PG, "qc_lock_source")
			run(c, fixture.Exec("SET lock_timeout='100ms'", 0))
			fixture.QueryTableCreateLockExec(t, p, b, c, "SELECT i INTO qc_lock_failed FROM qc_lock_source FOR UPDATE", "55P03", "canceling statement due to lock timeout", "qc_lock_source", tuple)
			run(c, fixture.Exec("RESET lock_timeout", 0))
			fixture.QueryTableCreateAbsent(t, func(s fixture.Step) { run(c, s) }, "qc_lock_failed")
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(c, fixture.Exec("SELECT i INTO qc_lock_failed FROM qc_lock_source FOR UPDATE", 1))
			run(c, fixture.Select("SELECT i FROM qc_lock_failed", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceQueryTableCreateCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
			run(a, fixture.Exec("CREATE TABLE qc_cancel_source(i INT)", 0))
			run(a, fixture.Exec("INSERT INTO qc_cancel_source VALUES(7)", 1))
			q := "CREATE TABLE qc_canceled AS SELECT i FROM qc_cancel_source FOR UPDATE"
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err := c.ExecContext(ctx, q)
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("precanceled result=%v error=%v", result, err)
				}
				rows, err := c.QueryContext(ctx, q)
				if rows != nil {
					rows.Close()
					t.Fatal("precanceled Query exposed cursor")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else {
				// Genuine server cancellation only after deterministic row-lock admission.
				tx, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				run(tx, fixture.Select("SELECT i FROM qc_cancel_source FOR UPDATE", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
				queryTableCreateCancelAdmitted(t, p.Ctx, c, q)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			fixture.QueryTableCreateAbsent(t, func(s fixture.Step) { run(c, s) }, "qc_canceled")
			run(c, fixture.Exec(q, 1))
			run(c, fixture.Select("SELECT i FROM qc_canceled", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func queryTableCreateCancelAdmitted(t *testing.T, parent context.Context, conn *sql.Conn, q string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	var pid int
	if err := conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	tuple := fixture.QueryTableCreateTuple(t, ctx, conn, true, "qc_cancel_source")
	type outcome struct {
		rows *sql.Rows
		err  error
	}
	done := make(chan outcome, 1)
	go func() { rows, err := conn.QueryContext(ctx, q); done <- outcome{rows, err} }()
	joined := false
	defer func() {
		if !joined {
			cancel()
			got := <-done
			if got.rows != nil {
				got.rows.Close()
			}
		}
	}()
	for {
		var waiting bool
		if err := fixture.Admin.QueryRowContext(ctx, "SELECT COALESCE((SELECT state='active' AND query=$2 AND wait_event_type='Lock' FROM pg_stat_activity WHERE pid=$1),false)", pid, q).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case got := <-done:
			joined = true
			if got.rows != nil {
				got.rows.Close()
			}
			t.Fatalf("completed before lock admission: %v", got.err)
		default:
		}
	}
	var signaled bool
	if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
		t.Fatalf("cancel signaled=%t err=%v", signaled, err)
	}
	got := <-done
	joined = true
	if got.rows != nil {
		got.rows.Close()
		t.Fatal("canceled Query exposed cursor")
	}
	fixture.QueryTableCreateLockDiagnostic(t, got.err, true, "57014", "canceling statement due to user request", "qc_cancel_source", tuple)
}
