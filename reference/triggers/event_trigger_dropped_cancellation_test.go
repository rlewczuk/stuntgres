package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDroppedObjectsCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, terminal := range []string{"precanceled", "expired"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", terminal, prepared), func(t *testing.T) {
				droppedScenario(t, func(d droppedCase) {
					// Fast pre-canceled pq execution has no portable no-output
					// guarantee. These are the documented native context routes;
					// the admitted callback below is shared with the real oracle.
					if d.b.PG {
						return
					}
					rs := droppedCancellationSetup(d)
					execute, closeCommand := droppedCancellationCommand(d, prepared)
					defer closeCommand()
					droppedCancellationPreserved(d, rs)
					var ctx context.Context
					var cancel context.CancelFunc
					want := context.Canceled
					if terminal == "expired" {
						ctx, cancel = context.WithDeadline(d.p.Ctx, time.Unix(0, 0))
						want = context.DeadlineExceeded
					} else {
						ctx, cancel = context.WithCancel(d.p.Ctx)
						cancel()
					}
					defer cancel()
					outcome := execute(ctx)
					if outcome.Result != nil || !errors.Is(outcome.Err, want) {
						t.Fatalf("terminal DROP result=%v error=%v, want nil/%v", outcome.Result, outcome.Err, want)
					}
					var state interface{ SQLState() string }
					if errors.As(outcome.Err, &state) {
						t.Fatalf("ordinary context error acquired SQLSTATE %q", state.SQLState())
					}
					// An elapsed source deadline may be classified by the operation
					// before Done closes. Settle independently, before cleanup cancel.
					select {
					case <-ctx.Done():
					case <-d.p.Ctx.Done():
						t.Fatal("terminal source did not settle", d.p.Ctx.Err())
					}
					if err := ctx.Err(); err != want {
						t.Fatalf("terminal source error=%v, want %v", err, want)
					}
					droppedCancellationPreserved(d, rs)
					droppedCancellationRecovered(d, rs, execute)
				})
			})
		}
		t.Run(fmt.Sprint("admitted_callback/prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				rs := droppedCancellationSetup(d)
				execute, closeCommand := droppedCancellationCommand(d, prepared)
				defer closeCommand()
				droppedCancellationPreserved(d, rs)
				// Keep the genuine audit reader before the real sleep admission:
				// cancellation must roll back a successfully materialized first bag.
				d.exec("CREATE OR REPLACE FUNCTION public.et_do_drop_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT public.et_do_read(1) INTO n; PERFORM pg_sleep(60); SELECT public.et_do_read(2) INTO n; INSERT INTO public.et_do_phase SELECT 'drop',count(*),0 FROM public.et_do_audit WHERE pass=1; END$$")
				// PostgreSQL 18.6 reports the canceled PERFORM and its event
				// function frame, independently of the preceding helper read.
				const cancellationContext = "SQL statement \"SELECT pg_sleep(60)\"\nPL/pgSQL function et_do_drop_fn() line 1 at PERFORM"
				fixture.SleepActiveCancelWithContext(t, d.p, d.b, d.c, droppedCancellationSQL, cancellationContext, nil, execute)
				droppedCancellationPreserved(d, rs)
				droppedCancellationRecovered(d, rs, execute)
			})
		})
	}
}

const droppedCancellationSQL = "DROP TABLE public.et_do_cancel"

func droppedCancellationSetup(d droppedCase) []droppedRecord {
	d.t.Helper()
	d.setup()
	d.exec("CREATE TABLE public.et_do_cancel(f1 integer PRIMARY KEY,f2 integer DEFAULT 42)")
	d.run(fixture.Exec("INSERT INTO public.et_do_cancel(f1) VALUES(7)", 1))
	rs := d.capture(droppedTable("public", "et_do_cancel", true))
	d.install("'DROP TABLE'")
	return rs
}

func droppedCancellationCommand(d droppedCase, prepared bool) (func(context.Context) fixture.RelationOutcome, func()) {
	d.t.Helper()
	var stmt *sql.Stmt
	if prepared {
		var err error
		stmt, err = d.c.PrepareContext(d.p.Ctx, droppedCancellationSQL)
		if err != nil {
			d.t.Fatal("prepare DROP", err)
		}
	}
	return func(ctx context.Context) fixture.RelationOutcome {
			var result sql.Result
			var err error
			if prepared {
				result, err = stmt.ExecContext(ctx)
			} else {
				result, err = d.c.ExecContext(ctx, droppedCancellationSQL)
			}
			return fixture.RelationOutcome{Result: result, Err: err}
		}, func() {
			if stmt != nil {
				stmt.Close()
			}
		}
}

func droppedCancellationPreserved(d droppedCase, rs []droppedRecord) {
	d.t.Helper()
	d.bag(1, nil)
	d.bag(2, nil)
	d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_audit", 0))
	d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 0))
	d.run(fixture.Select("SELECT f1,f2 FROM public.et_do_cancel", []string{"f1", "f2"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(42)}}))
	for _, r := range rs {
		d.run(eventTriggerCount("SELECT count(*) AS n FROM "+r.lookup+" AND oid="+fmt.Sprint(r.oid), 1))
	}
	droppedCancellationOutside(d)
}

func droppedCancellationOutside(d droppedCase) {
	d.t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("SELECT * FROM pg_event_trigger_dropped_objects()", droppedProtocolDiagnostic(""))
		s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
		d.run(s)
	}
}

func droppedCancellationRecovered(d droppedCase, rs []droppedRecord, execute func(context.Context) fixture.RelationOutcome) {
	d.t.Helper()
	// Replace the canceling callback with the existing genuine two-read body,
	// including its inside-sql_drop complete catalog-absence assertion.
	d.absenceCallback()
	outcome := execute(d.p.Ctx)
	if outcome.Err != nil || outcome.Result == nil {
		d.t.Fatalf("clean DROP result=%v error=%v", outcome.Result, outcome.Err)
	}
	if n, err := outcome.Result.RowsAffected(); err != nil || n != 0 {
		d.t.Fatalf("clean DROP affected=%d error=%v", n, err)
	}
	d.bag(1, rs)
	d.bag(2, rs)
	d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_audit", 14))
	d.phases(7)
	droppedCancellationOutside(d)
}
