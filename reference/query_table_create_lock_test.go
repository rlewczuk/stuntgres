package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestReferenceQueryTableCreateLockingCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, c := referenceLockConnections(t, p.ctx, b.db)
			run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, b.recorder, s) }
			run(a, referenceExec("CREATE TABLE qc_lock_source(i INT)", 0))
			run(a, referenceExec("INSERT INTO qc_lock_source VALUES(7)", 1))
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run(tx, referenceSelect("SELECT i FROM qc_lock_source FOR UPDATE", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(c, referenceExec("CREATE TABLE qc_lock_nodata AS SELECT i FROM qc_lock_source FOR UPDATE WITH NO DATA", 0))
			run(c, referenceSelect("SELECT i FROM qc_lock_nodata", []string{"i"}, []string{"INT4"}, nil))
			want := controlsReferenceDiagnostic("55P03", `could not obtain lock on row in relation "qc_lock_source"`)
			run(c, referenceErrorDiagnostic("CREATE TABLE qc_lock_failed AS SELECT i FROM qc_lock_source FOR UPDATE NOWAIT", want))
			queryTableCreateAbsent(t, func(s referenceStep) { run(c, s) }, b.pg, "qc_lock_failed")
			tuple := queryTableCreateTuple(t, p.ctx, c, b.pg, "qc_lock_source")
			run(c, referenceExec("SET lock_timeout='100ms'", 0))
			queryTableCreateLockExec(t, p, b, c, "SELECT i INTO qc_lock_failed FROM qc_lock_source FOR UPDATE", "55P03", "canceling statement due to lock timeout", "qc_lock_source", tuple)
			run(c, referenceExec("RESET lock_timeout", 0))
			queryTableCreateAbsent(t, func(s referenceStep) { run(c, s) }, b.pg, "qc_lock_failed")
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(c, referenceExec("SELECT i INTO qc_lock_failed FROM qc_lock_source FOR UPDATE", 1))
			run(c, referenceSelect("SELECT i FROM qc_lock_failed", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceQueryTableCreateCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, c := referenceLockConnections(t, p.ctx, b.db)
			run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, b.recorder, s) }
			run(a, referenceExec("CREATE TABLE qc_cancel_source(i INT)", 0))
			run(a, referenceExec("INSERT INTO qc_cancel_source VALUES(7)", 1))
			q := "CREATE TABLE qc_canceled AS SELECT i FROM qc_cancel_source FOR UPDATE"
			if !b.pg {
				ctx, cancel := context.WithCancel(p.ctx)
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
				tx, err := a.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				run(tx, referenceSelect("SELECT i FROM qc_cancel_source FOR UPDATE", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
				queryTableCreateCancelAdmitted(t, p.ctx, c, q)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			queryTableCreateAbsent(t, func(s referenceStep) { run(c, s) }, b.pg, "qc_canceled")
			run(c, referenceExec(q, 1))
			run(c, referenceSelect("SELECT i FROM qc_canceled", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
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
	tuple := queryTableCreateTuple(t, ctx, conn, true, "qc_cancel_source")
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
		if err := referenceAdmin.QueryRowContext(ctx, "SELECT COALESCE((SELECT state='active' AND query=$2 AND wait_event_type='Lock' FROM pg_stat_activity WHERE pid=$1),false)", pid, q).Scan(&waiting); err != nil {
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
	if err := referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
		t.Fatalf("cancel signaled=%t err=%v", signaled, err)
	}
	got := <-done
	joined = true
	if got.rows != nil {
		got.rows.Close()
		t.Fatal("canceled Query exposed cursor")
	}
	queryTableCreateLockDiagnostic(t, got.err, true, "57014", "canceling statement due to user request", "qc_cancel_source", tuple)
}
