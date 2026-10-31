package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The small request deliberately reuses the original active-cancellation SQL.
// [1,2]^3 gives 3,4,4,4,5,5,5,6, whose continuous median is exactly 4.5.
func TestReferenceOrderedSetAggregateExecutionPause(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared {
			route = "prepared"
		}
		t.Run(route, func(t *testing.T) {
			p := fixture.NewScenario(t)
			ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Second)
			defer cancel()
			p.Ctx = ctx
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle='postgres'", 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET lc_monetary='C'", 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE os_active(x INT)", 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO os_active SELECT generate_series(1,64)", 64))
					q := "SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY a.x+b.x+c.x DESC) AS v FROM (SELECT x FROM os_active ORDER BY x LIMIT $1) AS a CROSS JOIN (SELECT x FROM os_active ORDER BY x LIMIT $1) AS b CROSS JOIN (SELECT x FROM os_active ORDER BY x LIMIT $1) AS c"
					preparation := stuntgres.NewExecutionPause("query.aggregate.sort")
					defer preparation.Resume()
					stmt, err := c.PrepareContext(stuntgres.WithExecutionPause(p.Ctx, preparation), q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					orderedSetPauseUnadmitted(t, p.Ctx, preparation)
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					var source fixture.Executor = c
					if prepared {
						source = held
					}
					small := orderedSetValue(q, "FLOAT8", float64(4.5))
					small.Args = []any{int64(2)}
					// Both backends execute real preparation and both public result
					// routes with the fixture's raw, metadata and owned-row checks.
					fixture.RunStep(t, p.Ctx, source, b.Recorder, small)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, small)
					orderedSetPauseTerminals(t, p, b, c, source, small, prepared)
					if !b.PG {
						orderedSetPauseControls(t, p, b, held, source, small)
						orderedSetPauseIndependent(t, p, b, c, source, q, prepared, small)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, orderedSetValue("SELECT count(*) AS v FROM os_active", "INT8", int64(64)))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, small)
				})
			}
		})
	}
}

func orderedSetPauseControls(t *testing.T, p *fixture.Pair, b fixture.Backend, held fixture.HeldStatement, source fixture.Executor, small fixture.Step) {
	t.Helper()
	// One physical connection and one preheld statement, three fresh controls.
	for _, cycle := range []string{"success-before", "cancel", "success-after"} {
		t.Run(cycle, func(t *testing.T) {
			pause := stuntgres.NewExecutionPause("query.aggregate.sort")
			defer pause.Resume()
			ctx := stuntgres.WithExecutionPause(p.Ctx, pause)
			if cycle == "cancel" {
				w := newOrderedSetPauseWorker(ctx, pause)
				defer w.close(t)
				w.worker = t.Name()
				w.start(source, small.Query, small.Args)
				w.admission(t)
				w.profiles(t, p.Ctx, 3)
				w.cancel()
				w.canceled(t)
			} else {
				x := orderedSetPauseExecutor{Executor: source, T: t, Pause: pause}
				defer x.close(t)
				fixture.RunStep(t, ctx, &x, b.Recorder, small)
			}
			// Recovery is always the SAME preheld statement, even on direct.
			fixture.RunStep(t, p.Ctx, held, b.Recorder, small)
		})
	}

	for _, control := range []string{"absent", "inert", "wrong-point", "nested-disable", "early-resume"} {
		t.Run(control, func(t *testing.T) {
			pause := stuntgres.NewExecutionPause("query.aggregate.sort")
			defer pause.Resume()
			ctx := p.Ctx
			switch control {
			case "inert":
				// Merely constructing a pause does not attach it to work.
			case "wrong-point":
				pause = stuntgres.NewExecutionPause("query.aggregate.sort.other")
				defer pause.Resume()
				ctx = stuntgres.WithExecutionPause(ctx, pause)
			case "nested-disable":
				ctx = stuntgres.WithExecutionPause(stuntgres.WithExecutionPause(ctx, pause), nil)
			case "early-resume":
				pause.Resume()
				pause.Resume()
				ctx = stuntgres.WithExecutionPause(ctx, pause)
			}
			fixture.RunStep(t, ctx, source, b.Recorder, small)
			orderedSetPauseUnadmitted(t, p.Ctx, pause)
		})
	}

	t.Run("nested-replacement", func(t *testing.T) {
		outer := stuntgres.NewExecutionPause("query.aggregate.sort")
		inner := stuntgres.NewExecutionPause("query.aggregate.sort")
		defer outer.Resume()
		defer inner.Resume()
		ctx := stuntgres.WithExecutionPause(stuntgres.WithExecutionPause(p.Ctx, outer), inner)
		x := orderedSetPauseExecutor{Executor: source, T: t, Pause: inner}
		defer x.close(t)
		fixture.RunStep(t, ctx, &x, b.Recorder, small)
		orderedSetPauseUnadmitted(t, p.Ctx, outer)
	})

	t.Run("canceled-wait", func(t *testing.T) {
		pause := stuntgres.NewExecutionPause("query.aggregate.sort")
		defer pause.Resume()
		// Cancel observation before launch: after admission Wait is allowed to
		// select admission even if its observation context is also canceled.
		observe, stop := context.WithCancel(p.Ctx)
		stop()
		fixture.LexicalOrdinary(t, pause.Wait(observe), context.Canceled)
		x := orderedSetPauseExecutor{Executor: source, T: t, Pause: pause, Held: func(w *orderedSetPauseWorker) {
			w.pending(t)
			if err := pause.Wait(p.Ctx); err != nil {
				t.Fatal(err)
			}
			w.pending(t)
		}}
		defer x.close(t)
		fixture.RunStep(t, stuntgres.WithExecutionPause(p.Ctx, pause), &x, b.Recorder, small)
	})

	for _, expired := range []bool{false, true} {
		name := "precanceled"
		if expired {
			name = "expired"
		}
		t.Run(name, func(t *testing.T) {
			pause := stuntgres.NewExecutionPause("query.aggregate.sort")
			defer pause.Resume()
			ctx, stop := context.WithCancel(p.Ctx)
			want := context.Canceled
			if expired {
				stop()
				ctx, stop = context.WithDeadline(p.Ctx, time.Time{})
				want = context.DeadlineExceeded
			} else {
				stop()
			}
			defer stop()
			rows, err := source.QueryContext(stuntgres.WithExecutionPause(ctx, pause), small.Query, small.Args...)
			if rows != nil {
				rows.Close()
				t.Fatal("interrupted request published rows")
			}
			fixture.LexicalOrdinary(t, err, want)
			orderedSetPauseUnadmitted(t, p.Ctx, pause)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, small)
		})
	}
}

func orderedSetPauseIndependent(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source fixture.Executor, q string, prepared bool, small fixture.Step) {
	t.Helper()
	other, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	stmt, err := other.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var second fixture.Executor = other
	if prepared {
		second = fixture.HeldStatement{Executor: other, Statement: stmt, SQL: q}
	}
	a := stuntgres.NewExecutionPause("query.aggregate.sort")
	bPause := stuntgres.NewExecutionPause("query.aggregate.sort")
	defer a.Resume()
	defer bPause.Resume()
	// B's fixture-owned result is consumed only after A has canceled/joined.
	x := orderedSetPauseExecutor{Executor: second, T: t, Pause: bPause, Held: func(bWorker *orderedSetPauseWorker) {
		orderedSetPauseUnadmittedBeforeLaunch(t, p.Ctx, a)
		w := newOrderedSetPauseWorker(stuntgres.WithExecutionPause(p.Ctx, a), a)
		defer w.close(t)
		w.worker = t.Name() + "/A"
		w.start(source, q, small.Args)
		w.admission(t)
		bWorker.pending(t)
		w.profiles(t, p.Ctx, 1)
		bWorker.profiles(t, p.Ctx, 1)
		w.cancel()
		w.canceled(t)
		bWorker.pending(t)
		if err := bPause.Wait(p.Ctx); err != nil {
			t.Fatal(err)
		}
	}}
	defer x.close(t)
	fixture.RunStep(t, stuntgres.WithExecutionPause(p.Ctx, bPause), &x, b.Recorder, small)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, orderedSetValue("SELECT count(*) AS v FROM os_active", "INT8", int64(64)))
}

func orderedSetPauseTerminals(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source fixture.Executor, small fixture.Step, prepared bool) {
	t.Helper()
	// Identical SQL with empty/singleton input cannot compare aggregate tuples.
	for _, limit := range []int64{0, 1} {
		pause := stuntgres.NewExecutionPause("query.aggregate.sort")
		func() {
			defer pause.Resume()
			s := small
			s.Args = []any{limit}
			s.Rows = [][]any{{nil}}
			if limit == 1 {
				s.Rows = [][]any{{float64(3)}}
			}
			fixture.RunStep(t, stuntgres.WithExecutionPause(p.Ctx, pause), source, b.Recorder, s)
			orderedSetPauseUnadmitted(t, p.Ctx, pause)
		}()
	}
	// Reuse the existing ordered-set fixture and terminal SQL corpus. These
	// statements remain shared oracle checks, not native output goldens.
	orderedSetSetup(t, p, c, b)
	scalar := "SELECT percentile_cont($1::float8) WITHIN GROUP(ORDER BY x) AS v FROM os_input"
	nullScalar := orderedSetValue(scalar, "FLOAT8", nil)
	nullScalar.Args = []any{nil}
	invalidScalar := orderedSetDiagnostic(scalar, "22003", "percentile value 1.1 is not between 0 and 1", "", "")
	invalidScalar.Args = []any{float64(1.1)}
	invalidScalar.ErrorStage = "execute"
	steps := []fixture.Step{nullScalar, invalidScalar}
	for _, arg := range []string{"NULL::float8", "NULL::float8[]", "'{}'::float8[]", "'{1.1}'::float8[]"} {
		typ := "_FLOAT8"
		if arg == "NULL::float8" {
			typ = "FLOAT8"
		}
		steps = append(steps, orderedSetValue("SELECT percentile_cont("+arg+") WITHIN GROUP(ORDER BY x) AS v FROM os_input WHERE FALSE", typ, nil))
	}
	for _, tc := range []struct {
		arg   string
		value any
	}{
		{"NULL::float8[]", nil},
		{"'{}'::float8[]", "{}"},
		{"'{NULL,NULL}'::float8[]", "{NULL,NULL}"},
	} {
		steps = append(steps, orderedSetValue("SELECT percentile_cont("+tc.arg+") WITHIN GROUP(ORDER BY x)::text AS v FROM os_input", "TEXT", tc.value))
	}
	for _, fraction := range []string{"NULL::float8", "1.1::float8"} {
		s := orderedSetDiagnostic("SELECT percentile_cont("+fraction+") WITHIN GROUP(ORDER BY 1/(x-x)) AS v FROM os_input", "22012", "division by zero", "", "")
		s.ErrorStage = "execute"
		steps = append(steps, s)
	}
	for _, step := range steps {
		func() {
			pause := stuntgres.NewExecutionPause("query.aggregate.sort")
			defer pause.Resume()
			step.Prepared = prepared
			fixture.RunStep(t, stuntgres.WithExecutionPause(p.Ctx, pause), c, b.Recorder, step)
			orderedSetPauseUnadmitted(t, p.Ctx, pause)
		}()
	}
}
