package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryParallelSafety(t *testing.T) {
	queryParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) {
			if !t.Run(s.Query, func(t *testing.T) { queryParallelRun(t, p, b, c, s) }) {
				t.FailNow()
			}
		}
		queryParallelSeed(t, p, b, c)
		queryParallelWorkers(t, p, b, c, "7")
		run(fixture.Exec("CREATE TABLE parallel_write(id INT,k INT)", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO parallel_write SELECT id,k FROM parallel_rows", 6)))
		run(fixture.Exec("UPDATE parallel_write SET k=9 WHERE id=1", 1))
		run(fixture.Prepared(fixture.Exec("DELETE FROM parallel_write WHERE id=2", 1)))
		merge := fixture.Exec("MERGE INTO parallel_write w USING (SELECT 1 AS id,8 AS k) s ON w.id=s.id WHEN MATCHED THEN UPDATE SET k=s.k", 1)
		// As in fixture.MergeRun, pq does not decode the MERGE command tag.
		// Native must report one affected row; the following exact persisted
		// rows independently prove the update on both backends.
		if b.PG {
			merge.Affected = 0
		}
		run(merge)
		run(fixture.Select("SELECT id,k FROM parallel_write ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(8)}, {int64(3), int64(1)}, {int64(4), int64(2)}, {int64(5), nil}, {int64(6), int64(1)}}))
		run(fixture.ImplicitError("INSERT INTO parallel_write SELECT id,10/(id-id) FROM parallel_rows", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
		run(fixture.Select("SELECT id FROM parallel_write ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}}))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer peer.ExecContext(p.Ctx, "ROLLBACK")
		for _, strength := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Prepared(fixture.Select("SELECT id FROM parallel_rows WHERE id=1 FOR "+strength, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})))
			queryParallelRun(t, p, b, peer, fixture.Exec("BEGIN", 0))
			locked := fixture.ErrorDiagnostic("SELECT id FROM parallel_rows WHERE id=1 FOR UPDATE NOWAIT", queryParallelDiagnostic("55P03", "could not obtain lock on row in relation \"parallel_rows\""))
			locked.SelectRows = true
			queryParallelRun(t, p, b, peer, locked)
			queryParallelRun(t, p, b, peer, fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("COMMIT", 0))
			queryParallelRun(t, p, b, peer, fixture.Select("SELECT id FROM parallel_rows WHERE id=1 FOR UPDATE NOWAIT", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
		run(fixture.Exec("CREATE SEQUENCE parallel_draw START 10", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE parallel_lazy NO SCROLL CURSOR FOR SELECT id,nextval('parallel_draw') AS n FROM parallel_rows ORDER BY id", 0))
		state := func(n int64, called bool) {
			run(fixture.Select("SELECT last_value,is_called FROM parallel_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
		}
		state(10, false)
		run(fixture.Select("FETCH 1 FROM parallel_lazy", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}}))
		state(10, true)
		run(fixture.Select("FETCH 1 FROM parallel_lazy", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(11)}}))
		state(11, true)
		run(fixture.Exec("CLOSE parallel_lazy", 0))
		run(fixture.Exec("COMMIT", 0))
		// A hidden unsafe sort expression still draws once per input occurrence.
		run(fixture.Select("SELECT id FROM parallel_rows ORDER BY nextval('parallel_draw')", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}}))
		state(17, true)
		run(fixture.Select("SELECT nextval('parallel_draw') AS n FROM parallel_rows ORDER BY id", []string{"n"}, []string{"INT8"}, [][]any{{int64(18)}, {int64(19)}, {int64(20)}, {int64(21)}, {int64(22)}, {int64(23)}}))
		state(23, true)
		run(fixture.Select("SELECT setseed(0)", []string{"setseed"}, []string{"VOID"}, [][]any{{""}}))
		// Selected random calls are standalone projection roots in the existing
		// grammar. Compare their two per-row draws through named derived slots;
		// do not widen scalar-call grammar merely for this safety witness.
		run(fixture.Select("SELECT (lo>=0 AND hi<1) AS valid FROM (SELECT id,random() AS lo,random() AS hi FROM parallel_rows) sampled ORDER BY id", []string{"valid"}, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}, {true}, {true}}))
		// set_config uses the existing standalone SettingCall statement surface,
		// not a table projection with AS/FROM. Keep six independently checked
		// effects on the same owner without adding unrelated call grammar.
		for range 6 {
			run(fixture.Select("SELECT set_config('application_name','parallel_serial',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"parallel_serial"}}))
		}
		run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"parallel_serial"}}))
		for _, storage := range []string{"STORED", "VIRTUAL"} {
			name := "parallel_generated_" + storage
			run(fixture.Exec("CREATE TABLE "+name+"(id INT,g INT GENERATED ALWAYS AS (id+1) "+storage+")", 0))
			run(fixture.Exec("INSERT INTO "+name+"(id) VALUES(1),(2)", 2))
			run(fixture.Prepared(fixture.Select("SELECT id,g FROM "+name+" ORDER BY id", []string{"id", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}})))
		}
		run(fixture.Select("SELECT id,(SELECT k FROM parallel_write w WHERE w.id=r.id) AS k FROM parallel_rows r WHERE id=1", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(8)}}))
		run(fixture.Exec("CREATE TABLE parallel_cursor_div(i INT)", 0))
		run(fixture.Exec("INSERT INTO parallel_cursor_div VALUES(0),(2)", 2))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE parallel_fail NO SCROLL CURSOR FOR SELECT 10/i AS n FROM (SELECT i FROM parallel_cursor_div ORDER BY i DESC OFFSET 0) ordered", 0))
		run(fixture.Select("FETCH 1 FROM parallel_fail", []string{"n"}, []string{"INT4"}, [][]any{{int64(5)}}))
		for _, want := range []fixture.Diagnostic{queryParallelDiagnostic("22012", "division by zero"), queryParallelDiagnostic("55000", "portal \"parallel_fail\" cannot be run")} {
			run(fixture.Exec("SAVEPOINT parallel_cursor_error", 0))
			run(fixture.ErrorDiagnostic("FETCH 1 FROM parallel_fail", want))
			run(fixture.Exec("ROLLBACK TO parallel_cursor_error", 0))
			run(fixture.Exec("RELEASE parallel_cursor_error", 0))
		}
		run(fixture.Exec("CLOSE parallel_fail", 0))
		run(fixture.Exec("COMMIT", 0))
	})
}
