package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtremaSmoke(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE extrema_smoke(k TEXT,v INTEGER)", 0))
		run(fixture.Exec("INSERT INTO extrema_smoke VALUES('a',3),('a',NULL),('a',-2),('b',8),('b',8),(NULL,NULL)", 6))
		query := "SELECT min(v),max(v) FROM extrema_smoke"
		extremaBoth(t, p, conn, recorder, fixture.Select(query, []string{"min", "max"}, []string{"INT4", "INT4"}, [][]any{{int64(-2), int64(8)}}))
		s := fixture.Prepared(fixture.Select("SELECT k,min(v),max(v) FROM extrema_smoke GROUP BY k", []string{"k", "min", "max"}, []string{"TEXT", "INT4", "INT4"}, [][]any{{"a", int64(-2), int64(3)}, {"b", int64(8), int64(8)}, {nil, nil, nil}}))
		s.Bag = true
		run(s)
		extremaBoth(t, p, conn, recorder, fixture.Select(query+" WHERE FALSE", []string{"min", "max"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}))
		run(fixture.Exec("CREATE TABLE extrema_out(lo INTEGER,hi INTEGER)", 0))
		run(fixture.Exec("INSERT INTO extrema_out SELECT min(v),max(v) FROM extrema_smoke", 1))
		run(fixture.Select("SELECT lo,hi FROM extrema_out", []string{"lo", "hi"}, []string{"INT4", "INT4"}, [][]any{{int64(-2), int64(8)}}))
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		})
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
		fixture.RunStep(t, p.Ctx, held, recorder, fixture.Select(query, []string{"min", "max"}, []string{"INT4", "INT4"}, [][]any{{int64(-2), int64(8)}}))
		run(fixture.Exec("INSERT INTO extrema_smoke VALUES('c',-9),('c',12)", 2))
		fixture.RunStep(t, p.Ctx, held, recorder, fixture.Select(query, []string{"min", "max"}, []string{"INT4", "INT4"}, [][]any{{int64(-9), int64(12)}}))
		run(fixture.Exec("CREATE TABLE extrema_exact(n NUMERIC,s VARCHAR(4))", 0))
		run(fixture.Exec("INSERT INTO extrema_exact VALUES(123456789012345678901.2300,'z'),(-123456789012345678901.2300,'a')", 2))
		extremaBoth(t, p, conn, recorder, fixture.Select("SELECT min(n),max(s) FROM extrema_exact", []string{"min", "max"}, []string{"NUMERIC", "TEXT"}, [][]any{{"-123456789012345678901.2300", "z"}}))
		want := fixture.ControlsReferenceDiagnostic("42883", "function min(boolean) does not exist")
		want.Position = "8"
		want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		run(fixture.ErrorDiagnostic("SELECT min(TRUE) FROM extrema_smoke", want))
	})
}

func TestReferenceExtremaLateFailure(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE extrema_late(v TEXT)", 0))
		run(fixture.Exec("INSERT INTO extrema_late VALUES('1'),('bad')", 2))
		run(fixture.Exec("CREATE TABLE extrema_late_out(lo INTEGER,hi INTEGER)", 0))
		for _, query := range []string{
			"SELECT min(v::integer),max(v::integer) FROM extrema_late",
			"INSERT INTO extrema_late_out SELECT min(v::integer),max(v::integer) FROM extrema_late",
		} {
			t.Run(query, func(t *testing.T) {
				want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
				if query[:6] == "SELECT" {
					extremaDiagnostic(t, p, conn, recorder, query, want, "execute")
				}
				s := fixture.ErrorDiagnostic(query, want)
				s.ErrorStage = "execute"
				extremaBoth(t, p, conn, recorder, s)
			})
		}
		// RETURNING remains an independent native parser boundary, not a
		// supported late-failure consumer. PostgreSQL reaches the same cast.
		const returning = "INSERT INTO extrema_late_out SELECT min(v::integer),max(v::integer) FROM extrema_late RETURNING lo,hi"
		for _, prepared := range []bool{false, true} {
			want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
			stage := "execute"
			if !pg {
				want = fixture.ControlsReferenceDiagnostic("0A000", "INSERT suffix is unsupported")
				want.Position = fixture.StringAggPosition(returning, "RETURNING", false)
				if prepared {
					stage = "prepare"
				}
			}
			s := fixture.ErrorDiagnostic(returning, want)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, stage
			run(s)
		}
		run(fixture.Select("SELECT lo,hi FROM extrema_late_out", []string{"lo", "hi"}, []string{"INT4", "INT4"}, nil))
	})
}
