package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func tempAsScenario(t *testing.T, body func(*fixture.TempReferenceSession)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(fixture.NewTempReferenceSession(t, p, b.DB, b.Recorder, prepared)) })
			}
		})
	}
}
func tempAsRun(s *fixture.TempReferenceSession, c fixture.Executor, step fixture.Step) {
	s.T.Helper()
	step.Prepared = s.Prepared
	fixture.RunStep(s.T, s.P.Ctx, c, s.Record, step)
}
func tempAsExec(s *fixture.TempReferenceSession, c fixture.Executor, q string, n int64) {
	s.T.Helper()
	tempAsRun(s, c, fixture.Exec(q, n))
}
func tempAsAbsent(s *fixture.TempReferenceSession, name string) {
	s.T.Helper()
	fixture.QueryTableCreateAbsent(s.T, func(step fixture.Step) { tempAsRun(s, s.C, step) }, name)
}
func tempAsError(s *fixture.TempReferenceSession, c fixture.Executor, q, code, message, position string, analysis bool) {
	s.T.Helper()
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Position = position
	step := fixture.ErrorDiagnostic(q, d)
	step.ErrorStage = "execute"
	if analysis && s.Prepared {
		step.ErrorStage = "prepare"
	}
	tempAsRun(s, c, step)
}
func tempAsRows(s *fixture.TempReferenceSession, c fixture.Executor, name string, values ...int64) {
	fixture.TemporaryTableRows(s, c, name, values...)
}
func tempAsReproduction(s *fixture.TempReferenceSession) {
	tempAsExec(s, s.C, "CREATE TABLE grin_ctas_src (id int);", 0)
	tempAsExec(s, s.C, "INSERT INTO grin_ctas_src VALUES (1);", 1)
	tempAsExec(s, s.C, "BEGIN;", 0)
	tempAsExec(s, s.C, "CREATE TEMP TABLE grin_ctas ON COMMIT DROP AS SELECT * FROM grin_ctas_src;", 1)
	tempAsRun(s, s.C, fixture.Select("SELECT count(*) AS on_commit_rows FROM grin_ctas;", []string{"on_commit_rows"}, []string{"INT8"}, [][]any{{int64(1)}}))
	tempAsExec(s, s.C, "COMMIT;", 0)
	s.Missing(s.C, "grin_ctas", "id", "16")
	tempAsAbsent(s, "grin_ctas")
	tempAsExec(s, s.C, "CREATE TEMP TABLE grin_ctas2 AS SELECT * FROM grin_ctas_src;", 1)
	tempAsRun(s, s.C, fixture.Select("SELECT count(*) AS temp_rows FROM grin_ctas2;", []string{"temp_rows"}, []string{"INT8"}, [][]any{{int64(1)}}))
	tempAsExec(s, s.C, "BEGIN", 0)
	tempAsExec(s, s.C, "COMMIT", 0)
	s.Missing(s.Peer, "grin_ctas2", "id", "16")
	tempAsRun(s, s.C, fixture.Select("SELECT id FROM grin_ctas_src", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
}
func TestReferenceCreateTempTableAsReproduction(t *testing.T) { tempAsScenario(t, tempAsReproduction) }
func TestReferenceCreateTempTableAsCreation(t *testing.T) {
	tempAsScenario(t, func(s *fixture.TempReferenceSession) {
		tempAsExec(s, s.C, "CREATE TABLE ta_source(v INT,s TEXT)", 0)
		tempAsExec(s, s.C, "INSERT INTO ta_source VALUES(1,'a'),(1,'a'),(2,NULL)", 3)
		for i, q := range []string{"CREATE TEMP TABLE ta_copy AS SELECT * FROM ta_source", "CREATE TEMPORARY TABLE ta_copy_explicit AS SELECT * FROM ta_source WITH DATA"} {
			tempAsExec(s, s.C, q, 3)
			name := []string{"ta_copy", "ta_copy_explicit"}[i]
			step := fixture.Select("SELECT v,s FROM "+name, []string{"v", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}})
			step.Bag = true
			tempAsRun(s, s.C, step)
		}
		tempAsExec(s, s.C, "UPDATE ta_source SET v=9", 3)
		tempAsExec(s, s.C, "DROP TABLE ta_source", 0)
		tempAsRows(s, s.C, "ta_copy", 1, 1, 2)
		tempAsExec(s, s.C, "CREATE SEQUENCE ta_draw START 10", 0)
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_drawn AS SELECT nextval('ta_draw') AS n FROM ta_copy", 3)
		tempAsRun(s, s.C, fixture.Select("SELECT n FROM ta_drawn ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}, {int64(11)}, {int64(12)}}))
		tempAsRun(s, s.C, fixture.Select("SELECT nextval('ta_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(13)}}))
		tempAsRun(s, s.C, fixture.Select("CREATE TEMP TABLE ta_query_route AS SELECT 4 AS v", []string{}, []string{}, nil))
		tempAsRows(s, s.C, "ta_query_route", 4)
	})
}
func TestReferenceCreateTempTableAsIsolation(t *testing.T) {
	tempAsScenario(t, func(s *fixture.TempReferenceSession) {
		tempAsExec(s, s.C, "CREATE TABLE public.ta_shadow(v INT)", 0)
		tempAsExec(s, s.C, "INSERT INTO public.ta_shadow VALUES(7)", 1)
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_shadow AS SELECT 11 AS v", 1)
		tempAsRows(s, s.C, "ta_shadow", 11)
		tempAsRows(s, s.C, "public.ta_shadow", 7)
		tempAsRows(s, s.Peer, "ta_shadow", 7)
		tempAsExec(s, s.C, "BEGIN", 0)
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_private AS SELECT 11 AS v", 1)
		s.Missing(s.Peer, "ta_private", "v", "15")
		tempAsExec(s, s.C, "COMMIT", 0)
		s.Missing(s.Peer, "ta_private", "v", "15")
		tempAsExec(s, s.Peer, "CREATE TEMPORARY TABLE ta_private AS SELECT 22 AS v", 1)
		tempAsRows(s, s.C, "ta_private", 11)
		tempAsRows(s, s.Peer, "ta_private", 22)
		tempAsExec(s, s.C, "CREATE TABLE pg_temp.ta_alias AS SELECT 31 AS v", 1)
		tempAsRows(s, s.C, "pg_temp.ta_alias", 31)
		var own string
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.oid='ta_private'::regclass").Scan(&own); err != nil {
			s.T.Fatal(err)
		}
		tempAsExec(s, s.C, "CREATE TABLE "+own+".ta_actual AS SELECT 32 AS v", 1)
		tempAsRows(s, s.C, "pg_temp.ta_actual", 32)
		tempAsRun(s, s.C, fixture.Select("SELECT relpersistence::text AS p FROM pg_class WHERE oid='ta_private'::regclass", []string{"p"}, []string{"TEXT"}, [][]any{{"t"}}))
	})
}
func tempAsLifecycle(s *fixture.TempReferenceSession) {
	for _, c := range []struct{ name, clause string }{{"ta_preserve", ""}, {"ta_explicit", " ON COMMIT PRESERVE ROWS"}} {
		tempAsExec(s, s.C, "BEGIN", 0)
		tempAsExec(s, s.C, "CREATE TEMPORARY TABLE "+c.name+c.clause+" AS SELECT 1 AS v", 1)
		tempAsRows(s, s.C, c.name, 1)
		tempAsExec(s, s.C, "COMMIT", 0)
		tempAsExec(s, s.C, "BEGIN", 0)
		tempAsExec(s, s.C, "COMMIT", 0)
		tempAsRows(s, s.C, c.name, 1)
	}
	tempAsExec(s, s.C, "BEGIN", 0)
	tempAsExec(s, s.C, "CREATE TEMP TABLE ta_delete ON COMMIT DELETE ROWS AS SELECT v FROM (VALUES(1),(2)) q(v)", 2)
	tempAsRows(s, s.C, "ta_delete", 1, 2)
	tempAsExec(s, s.C, "COMMIT", 0)
	tempAsRows(s, s.C, "ta_delete")
	tempAsExec(s, s.C, "BEGIN", 0)
	tempAsExec(s, s.C, "INSERT INTO ta_delete VALUES(3)", 1)
	tempAsRows(s, s.C, "ta_delete", 3)
	tempAsExec(s, s.C, "COMMIT", 0)
	tempAsRows(s, s.C, "ta_delete")
	tempAsExec(s, s.C, "BEGIN", 0)
	tempAsExec(s, s.C, "CREATE TEMP TABLE ta_drop ON COMMIT DROP AS SELECT v FROM (VALUES(1),(2)) q(v)", 2)
	tempAsRows(s, s.C, "ta_drop", 1, 2)
	tempAsExec(s, s.C, "COMMIT", 0)
	s.Missing(s.C, "ta_drop", "v", "15")
	tempAsAbsent(s, "ta_drop")
	tempAsExec(s, s.C, "CREATE TEMP TABLE ta_auto_delete ON COMMIT DELETE ROWS AS SELECT v FROM (VALUES(1),(2)) q(v)", 2)
	tempAsRows(s, s.C, "ta_auto_delete")
	tempAsExec(s, s.C, "CREATE TEMP TABLE ta_auto_drop ON COMMIT DROP AS SELECT v FROM (VALUES(1),(2)) q(v)", 2)
	s.Missing(s.C, "ta_auto_drop", "v", "15")
	tempAsAbsent(s, "ta_auto_drop")
}
func TestReferenceCreateTempTableAsLifecycle(t *testing.T) { tempAsScenario(t, tempAsLifecycle) }
func TestReferenceCreateTempTableAsRollback(t *testing.T) {
	tempAsScenario(t, func(s *fixture.TempReferenceSession) {
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_kept AS SELECT 7 AS v", 1)
		tx, err := s.C.BeginTx(s.P.Ctx, nil)
		if err != nil {
			s.T.Fatal(err)
		}
		defer tx.Rollback()
		tempAsExec(s, tx, "CREATE TEMP TABLE ta_undone AS SELECT 1 AS v", 1)
		tempAsExec(s, tx, "INSERT INTO ta_kept VALUES(8)", 1)
		if err := tx.Rollback(); err != nil {
			s.T.Fatal(err)
		}
		tempAsAbsent(s, "ta_undone")
		tempAsRows(s, s.C, "ta_kept", 7)
		tempAsExec(s, s.C, "BEGIN", 0)
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_before ON COMMIT DROP AS SELECT 1 AS v", 1)
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_clear ON COMMIT DELETE ROWS AS SELECT 1 AS v", 1)
		tempAsExec(s, s.C, "SAVEPOINT mark", 0)
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_after ON COMMIT DROP AS SELECT 2 AS v", 1)
		tempAsExec(s, s.C, "INSERT INTO ta_clear VALUES(2)", 1)
		tempAsExec(s, s.C, "ROLLBACK TO mark", 0)
		tempAsRows(s, s.C, "ta_before", 1)
		tempAsRows(s, s.C, "ta_clear", 1)
		tempAsAbsent(s, "ta_after")
		tempAsExec(s, s.C, "COMMIT", 0)
		tempAsAbsent(s, "ta_before")
		tempAsRows(s, s.C, "ta_clear")
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_after AS SELECT 9 AS v", 1)
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_undone AS SELECT 8 AS v", 1)
		tempAsRows(s, s.C, "ta_after", 9)
		tempAsRows(s, s.C, "ta_undone", 8)
	})
}
func tempAsNoData(s *fixture.TempReferenceSession) {
	for i, body := range []string{"SELECT 1/0 AS x", "SELECT 'bad'::text::integer AS x", "SELECT (SELECT 1/0) AS x"} {
		name := []string{"ta_no_div", "ta_no_cast", "ta_no_sub"}[i]
		tempAsExec(s, s.C, "CREATE TEMP TABLE "+name+" AS "+body+" WITH NO DATA", 0)
		tempAsRun(s, s.C, fixture.Select("SELECT x FROM "+name, []string{"x"}, []string{"INT4"}, nil))
	}
	tempAsExec(s, s.C, "CREATE SEQUENCE ta_no_draw START 10", 0)
	tempAsExec(s, s.C, "CREATE TEMP TABLE ta_no_seq AS SELECT nextval('ta_no_draw') AS n WITH NO DATA", 0)
	tempAsRun(s, s.C, fixture.Select("SELECT nextval('ta_no_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
	q := "CREATE TEMP TABLE ta_no_param AS SELECT 1/$1::integer AS x WITH NO DATA"
	stmt, err := s.C.PrepareContext(s.P.Ctx, q)
	if err != nil {
		s.T.Fatal(err)
	}
	defer stmt.Close()
	tempAsAbsent(s, "ta_no_param")
	for _, args := range [][]any{nil, {1, 2}} {
		result, err := stmt.ExecContext(s.P.Ctx, args...)
		want := "sql: expected 1 arguments, got 0"
		if len(args) == 2 {
			want = "sql: expected 1 arguments, got 2"
		}
		if result != nil || err == nil || err.Error() != want {
			s.T.Fatalf("argument count result=%v err=%v", result, err)
		}
	}
	h := fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: q}
	fixture.RunStep(s.T, s.P.Ctx, h, s.Record, fixture.Exec(q, 0, 0))
	tempAsExec(s, s.C, "DROP TABLE ta_no_param", 0)
	invalid := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
	invalid.ErrorStage = "execute"
	fixture.RunStep(s.T, s.P.Ctx, h, s.Record, invalid)
	tempAsAbsent(s, "ta_no_param")
	tempAsExec(s, s.C, "BEGIN", 0)
	tempAsExec(s, s.C, "CREATE TEMP TABLE ta_no_drop ON COMMIT DROP AS SELECT 1/0 AS v WITH NO DATA", 0)
	tempAsRows(s, s.C, "ta_no_drop")
	tempAsExec(s, s.C, "COMMIT", 0)
	tempAsAbsent(s, "ta_no_drop")
	tempAsExec(s, s.C, "CREATE TEMP TABLE ta_no_delete ON COMMIT DELETE ROWS AS SELECT 1/0 AS v WITH NO DATA", 0)
	tempAsRows(s, s.C, "ta_no_delete")
}
func TestReferenceCreateTempTableAsNoData(t *testing.T) { tempAsScenario(t, tempAsNoData) }
