package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func movingFailureSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, phase string, sleep bool) {
	t.Helper()
	movingSetup(t, p, b, c)
	lifecycleExec(t, p, b, c, "CREATE TABLE ma_effects(v integer)", "CREATE TABLE ma_destination(id integer,v integer)")
	createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ma_destination VALUES(99,99)", 1))
	forward, inverse, final := "$1+$2", "$1-$2", "$1"
	trigger := "$2=5"
	if phase == "inverse" {
		trigger = "$2=2"
	}
	if phase == "final" {
		trigger = "$1=8"
	}
	failure := "1/(CASE WHEN " + trigger + " THEN 0 ELSE 1 END)"
	if sleep {
		failure = "CASE WHEN pg_sleep(CASE WHEN " + trigger + " THEN 30 ELSE 0 END) IS NULL THEN 1 ELSE 1 END"
	}
	switch phase {
	case "forward":
		forward = "($1+$2)*(" + failure + ")"
	case "inverse":
		inverse = "($1-$2)*(" + failure + ")"
	case "final":
		final = "$1*(" + failure + ")"
	}
	for _, support := range []struct{ name, args, body string }{
		{"ma_forward", "integer,integer", forward},
		{"ma_inverse", "integer,integer", inverse},
		{"ma_final", "integer", final},
	} {
		body := "SELECT " + support.body
		if support.name == "ma_"+phase {
			body = "INSERT INTO ma_effects VALUES($1); " + body
		}
		lifecycleExec(t, p, b, c, "CREATE FUNCTION "+support.name+"("+support.args+") RETURNS integer BEGIN ATOMIC "+body+"; END")
	}
	lifecycleExec(t, p, b, c, "CREATE AGGREGATE ma_failure(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=ma_forward,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0',MFINALFUNC=ma_final)")
}

func movingAtomicState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM ma_effects", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	createAggregateBoth(t, p, b, c, fixture.Select("SELECT id,v FROM ma_destination ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
}

func TestReferenceAggregateMovingAtomicity(t *testing.T) {
	for _, phase := range []string{"forward", "inverse", "final"} {
		t.Run(phase, func(t *testing.T) {
			createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				movingFailureSetup(t, p, b, c, phase, false)
				const q = "SELECT id,ma_failure(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input WHERE id<=$1 ORDER BY v,id"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				const insert = "INSERT INTO ma_destination SELECT id,ma_failure(x) OVER(ORDER BY id ROWS 1 PRECEDING) FROM ma_input WHERE id<=$1"
				for route := range 3 {
					d := fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: "SQL function \"ma_" + phase + "\" statement 2"}
					s := fixture.ImplicitError(q, d, 4)
					s.SelectRows, s.ErrorStage = true, "execute"
					if route == 2 {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					} else {
						s.Prepared = route == 1
						createAggregateRun(t, p, b, c, s)
					}
					movingAtomicState(t, p, b, c)
					failure := fixture.ImplicitError(insert, d, 4)
					failure.Prepared, failure.ErrorStage = route != 0, "execute"
					createAggregateRun(t, p, b, c, failure)
					movingAtomicState(t, p, b, c)
					recovery := movingRows(q, int64(2), int64(5))
					recovery.Args = []any{int64(2)}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, recovery)
					count := int64(2)
					if phase == "inverse" {
						count = 0
					}
					createAggregateRun(t, p, b, c, fixture.Exec("DELETE FROM ma_effects", count))
				}
			})
		})
	}
}

func TestReferenceAggregateMovingCancellation(t *testing.T) {
	for _, phase := range []string{"forward", "inverse", "final"} {
		for _, prepared := range []bool{false, true} {
			name := phase + "/direct"
			if prepared {
				name = phase + "/held"
			}
			t.Run(name, func(t *testing.T) {
				createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					movingFailureSetup(t, p, b, c, phase, true)
					const q = "INSERT INTO ma_destination SELECT id,ma_failure(x) OVER(ORDER BY id ROWS 1 PRECEDING) FROM ma_input WHERE id<=$1"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL function \"ma_"+phase+"\" statement 2", nil, func(ctx context.Context) fixture.RelationOutcome {
						var result sql.Result
						var err error
						if prepared {
							result, err = stmt.ExecContext(ctx, 4)
						} else {
							result, err = c.ExecContext(ctx, q, 4)
						}
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					movingAtomicState(t, p, b, c)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 2, 2))
					createAggregateBoth(t, p, b, c, fixture.Select("SELECT id,v FROM ma_destination ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(5)}, {int64(99), int64(99)}}))
				})
			})
		}
	}
}
