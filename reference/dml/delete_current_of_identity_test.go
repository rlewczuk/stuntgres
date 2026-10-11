package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func currentOfReference(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	updateFromReference(t, body)
}

func currentOfSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0))
	run(fixture.Exec("INSERT INTO t VALUES(3,'three'),(1,'one'),(2,'two')", 3))
}
func currentOfFetch(q string, ids ...int64) fixture.Step {
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	return fixture.Select(q, []string{"id"}, []string{"INT4"}, rows)
}
func currentOfError(q, code, message string, prepared bool) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	s.Prepared = prepared
	s.ErrorStage = "execute"
	return s
}
func currentOfFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	run := updateFromRun(t, p, b, c)
	run(fixture.Exec("SAVEPOINT diagnose", 0))
	fixture.DeleteReferenceExecuteStep(t, p, c, b.Recorder, s)
	run(fixture.Exec("ROLLBACK TO diagnose", 0))
	run(fixture.Exec("RELEASE diagnose", 0))
}

func TestReferenceDeleteCurrentOfPositionedRow(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, returning := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+map[bool]string{false: "/exec", true: "/returning"}[returning], func(t *testing.T) {
				currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					currentOfSeed(run)
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t ORDER BY id FOR UPDATE", 0))
					run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
					s := fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1)
					if returning {
						s = fixture.Select(s.Query+" RETURNING id,v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}})
					}
					s.Prepared = prepared
					run(s)
					fixture.DeleteReferenceRows(t, p, c, b.Recorder, 2, 3)
					run(currentOfFetch("FETCH FORWARD 1 FROM c", 2))
					run(fixture.Exec("COMMIT", 0))
					fixture.DeleteReferenceRows(t, p, c, b.Recorder, 2, 3)
				})
			})
		}
	}
	// Complete migration of the original locked-cursor committed survivor witness.
	t.Run("migrated_survivor", func(t *testing.T) {
		currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := updateFromRun(t, p, b, tx)
			tr(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t ORDER BY id FOR UPDATE", 0))
			tr(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
			tr(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
			fixture.DeleteReferenceRows(t, p, tx, b.Recorder, 2)
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			fixture.DeleteReferenceRows(t, p, c, b.Recorder, 2)
		})
	})
	t.Run("query_without_returning", func(t *testing.T) {
		currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			currentOfSeed(run)
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("DELETE FROM t WHERE CURRENT OF c", []string{}, []string{}, nil)
				s.Prepared = prepared
				run(s)
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 2, 3)
			}
		})
	})
}

func TestReferenceDeleteCurrentOfPreparationAndMetadata(t *testing.T) {
	currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		currentOfSeed(run)
		q := `DELETE FROM public.t AS d WHERE CURRENT OF "MiX" RETURNING d.id,$1::text AS label`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("quiet prepare before DECLARE", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("SAVEPOINT missing", 0))
		result, err := stmt.ExecContext(p.Ctx, "first")
		if result != nil {
			t.Fatal("missing cursor exposed result")
		}
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "34000", Message: `cursor "MiX" does not exist`})
		run(fixture.Exec("ROLLBACK TO missing", 0))
		for _, id := range []int64{1, 2} {
			run(fixture.Exec(`DECLARE "MiX" CURSOR FOR SELECT 99 AS projected FROM t WHERE id=$1`, 0, id))
			run(fixture.Select(`FETCH FORWARD 1 FROM "MiX"`, []string{"projected"}, []string{"INT4"}, [][]any{{int64(99)}}))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{id, "owned"}}, "owned"))
			run(fixture.Exec(`CLOSE "MiX"`, 0))
		}
		fixture.DeleteReferenceRows(t, p, c, b.Recorder, 3)
		run(fixture.Exec("ROLLBACK", 0))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ q, code, msg, token string }{
				{"DELETE FROM absent WHERE CURRENT OF missing", "42P01", `relation "absent" does not exist`, "absent"},
				{"DELETE FROM t WHERE CURRENT OF missing RETURNING absent", "42703", `column "absent" does not exist`, "absent"},
				{"DELETE FROM t USING absent WHERE CURRENT OF missing", "42P01", `relation "absent" does not exist`, "absent"},
				{`DELETE FROM t WHERE CURRENT OF "é" AND TRUE`, "42601", `syntax error at or near "AND"`, "AND"},
				{`DELETE FROM t WHERE "current" OF c`, "42601", `syntax error at or near "OF"`, "OF"},
			} {
				updateFromDiagnostic(t, p.Ctx, c, tc.q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.msg, Position: updateFromPosition(tc.q, tc.token)}, prepared)
			}
		}
		t.Run("refcursor_parameter_boundary", func(t *testing.T) {
			run := updateFromRun(t, p, b, c)
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE parameter_c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM parameter_c", 1))
			q := "DELETE FROM t WHERE CURRENT OF $1 RETURNING id"
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec("SAVEPOINT raw_parameter", 0))
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "$1"`, Position: "32"})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				run(fixture.Exec("ROLLBACK TO raw_parameter", 0))
				run(fixture.Exec("RELEASE raw_parameter", 0))
			}
		})
	})
}

func TestReferenceDeleteCurrentOfDuplicateIdentityAndLastFetch(t *testing.T) {
	t.Run("duplicates", func(t *testing.T) {
		currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(v INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7),(7)", 2))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT 42 AS projected FROM t", 0))
			run(fixture.Select("FETCH FORWARD 1 FROM c", []string{"projected"}, []string{"INT4"}, [][]any{{int64(42)}}))
			run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
			run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 0))
			run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	})
	t.Run("zero_width", func(t *testing.T) {
		currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t()", 0))
			run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
			run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT * FROM t", 0))
			run(fixture.Select("FETCH FORWARD 1 FROM c", []string{}, []string{}, [][]any{{}}))
			run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
			run(fixture.Select("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		})
	})
	t.Run("last_and_replacement", func(t *testing.T) {
		currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			currentOfSeed(run)
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t ORDER BY id FOR UPDATE", 0))
			run(currentOfFetch("FETCH 2 FROM c", 1, 2))
			run(currentOfFetch("DELETE FROM t WHERE CURRENT OF c RETURNING id", 2))
			run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 0))
			run(currentOfFetch("FETCH 1 FROM c", 3))
			run(fixture.Exec("DELETE FROM t WHERE id=3", 1))
			run(fixture.Exec("INSERT INTO t VALUES(3,'three')", 1))
			run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 0))
			fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 3)
			run(currentOfFetch("FETCH FORWARD 1 FROM c"))
			currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t WHERE CURRENT OF c", "24000", `cursor "c" is not positioned on a row`, false))
		})
	})
}
