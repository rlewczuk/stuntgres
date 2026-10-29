package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerBeforeStatementTruncate(t *testing.T) {
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE bst_a(id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY)", "CREATE TABLE bst_b(id integer GENERATED ALWAYS AS IDENTITY,aid integer REFERENCES bst_a)",
			"CREATE TABLE bst_trlog(seq integer GENERATED ALWAYS AS IDENTITY,rel text,name text,a bigint,b bigint,sa bigint,sb bigint)",
			"CREATE FUNCTION bst_trunc() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_trlog(rel,name,a,b,sa,sb) VALUES(TG_TABLE_NAME,TG_NAME,(SELECT count(*) FROM bst_a),(SELECT count(*) FROM bst_b),(SELECT last_value FROM bst_a_id_seq),(SELECT last_value FROM bst_b_id_seq)); RETURN NULL; END$$",
			"CREATE FUNCTION bst_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$")
		run(fixture.Exec("INSERT INTO bst_a DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO bst_b(aid) VALUES(1)", 1))
		run(fixture.Select("SELECT setval('bst_a_id_seq',5) AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
		run(fixture.Select("SELECT setval('bst_b_id_seq',7) AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}}))
		for _, rel := range []string{"bst_a", "bst_b"} {
			for _, name := range []string{"z", "a"} {
				userOperatorExec(t, p, b, c, "CREATE TRIGGER "+name+" BEFORE TRUNCATE ON "+rel+" EXECUTE FUNCTION bst_trunc()")
			}
			userOperatorExec(t, p, b, c, "CREATE TRIGGER wrong BEFORE DELETE ON "+rel+" EXECUTE FUNCTION bst_fail()")
		}
		run(fixture.Exec("TRUNCATE bst_a,bst_b RESTART IDENTITY", 0))
		want := [][]any{}
		for _, rel := range []string{"bst_a", "bst_b"} {
			for _, name := range []string{"a", "z"} {
				want = append(want, []any{rel, name, int64(1), int64(1), int64(5), int64(7)})
			}
		}
		run(fixture.Select("SELECT rel,name,a,b,sa,sb FROM bst_trlog ORDER BY seq", []string{"rel", "name", "a", "b", "sa", "sb"}, []string{"TEXT", "TEXT", "INT8", "INT8", "INT8", "INT8"}, want))
		run(beforeStatementCount("bst_a", 0))
		run(beforeStatementCount("bst_b", 0))
		run(fixture.Select("SELECT nextval('bst_a_id_seq') AS a,nextval('bst_b_id_seq') AS b", []string{"a", "b"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
		userOperatorExec(t, p, b, c, "TRUNCATE bst_trlog")
		run(fixture.Exec("INSERT INTO bst_a DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO bst_b DEFAULT VALUES", 1))
		userOperatorExec(t, p, b, c, "CREATE TRIGGER zz BEFORE TRUNCATE ON bst_b EXECUTE FUNCTION bst_fail()")
		d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		d.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function bst_fail() line 1 at PERFORM"
		run(fixture.ErrorDiagnostic("TRUNCATE bst_a RESTART IDENTITY CASCADE", d))
		run(beforeStatementCount("bst_a", 1))
		run(beforeStatementCount("bst_b", 1))
		run(beforeStatementCount("bst_trlog", 0))
		run(fixture.Select("SELECT last_value FROM bst_a_id_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
		userOperatorExec(t, p, b, c, "DROP TRIGGER zz ON bst_b")
		run(fixture.Exec("TRUNCATE bst_a CASCADE", 0))
		want = nil
		for _, rel := range []string{"bst_a", "bst_b"} {
			for _, name := range []string{"a", "z"} {
				want = append(want, []any{rel, name})
			}
		}
		run(fixture.Select("SELECT rel,name FROM bst_trlog ORDER BY seq", []string{"rel", "name"}, []string{"TEXT", "TEXT"}, want))
		userOperatorExec(t, p, b, c, "CREATE TABLE bst_tp(id integer) PARTITION BY RANGE(id)", "CREATE TABLE bst_tl PARTITION OF bst_tp FOR VALUES FROM(0) TO(10)", "CREATE TRIGGER s BEFORE TRUNCATE ON bst_tp EXECUTE FUNCTION bst_trunc()", "CREATE TRIGGER s BEFORE TRUNCATE ON bst_tl EXECUTE FUNCTION bst_trunc()", "TRUNCATE bst_trlog")
		run(fixture.Exec("TRUNCATE bst_tp,bst_tl", 0))
		run(fixture.Select("SELECT rel,name FROM bst_trlog ORDER BY seq", []string{"rel", "name"}, []string{"TEXT", "TEXT"}, [][]any{{"bst_tp", "s"}, {"bst_tl", "s"}}))
	})
}
func TestReferenceTriggerBeforeStatementAtomicity(t *testing.T) {
	for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
		t.Run(op, func(t *testing.T) {
			beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				beforeStatementSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO bst(id,v) VALUES(1,10)", 1))
				userOperatorExec(t, p, b, c, "CREATE FUNCTION bst_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_log(name) VALUES('failure'); PERFORM 1/0; RETURN NULL; END$$", "CREATE TRIGGER a BEFORE "+op+" ON bst EXECUTE FUNCTION bst_log()", "CREATE TRIGGER z BEFORE "+op+" ON bst EXECUTE FUNCTION bst_fail()")
				q := "INSERT INTO bst(id,v) VALUES(2,20)"
				if op == "UPDATE" {
					q = "UPDATE bst SET v=20"
				}
				if op == "DELETE" {
					q = "DELETE FROM bst"
				}
				d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				d.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function bst_fail() line 1 at PERFORM"
				run(fixture.ErrorDiagnostic(q, d))
				f := fixture.ErrorDiagnostic(q+" RETURNING id,v", d)
				f.SelectRows = true
				run(f)
				userOperatorExec(t, p, b, c, "BEGIN", "SAVEPOINT s")
				run(fixture.ErrorDiagnostic(q, d))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
				userOperatorExec(t, p, b, c, "ROLLBACK TO s")
				run(beforeStatementCount("bst_log", 0))
				run(triggerRows("SELECT id,v FROM bst", []any{int64(1), int64(10)}))
				userOperatorExec(t, p, b, c, "DROP TRIGGER z ON bst")
				run(fixture.Exec(q, 1))
				run(beforeStatementCount("bst_log", 1))
				userOperatorExec(t, p, b, c, "COMMIT")
			})
		})
	}
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		beforeStatementSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TABLE bst_caught(n integer)", "CREATE TABLE bst_inner(id integer)", "CREATE TRIGGER s BEFORE INSERT ON bst_inner EXECUTE FUNCTION bst_log()", "CREATE FUNCTION bst_catch() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE n integer:=0; BEGIN BEGIN INSERT INTO bst_inner VALUES(1); PERFORM 1/0; EXCEPTION WHEN division_by_zero THEN n:=n+1; END; INSERT INTO bst_caught VALUES(n); INSERT INTO bst_inner VALUES(2); RETURN NULL; END$$", "CREATE TRIGGER outer_s BEFORE INSERT ON bst EXECUTE FUNCTION bst_catch()")
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES($1,10)", 1, 1))
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES($1,10)", 1, 2))
		run(fixture.Select("SELECT n FROM bst_caught", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
		run(beforeStatementCount("bst_inner", 2))
		run(beforeStatementCount("bst_log", 2))
	})
}
func TestReferenceTriggerBeforeStatementReturns(t *testing.T) {
	for _, expr := range []string{"NULL", "NEW", "ROW(1)", "ROW(1,2,3,4)", "ROW(1,2,3,4,5)"} {
		t.Run(expr, func(t *testing.T) {
			beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				beforeStatementSetup(t, p, b, c)
				userOperatorExec(t, p, b, c, "CREATE FUNCTION bst_return() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN "+expr+"; END$$", "CREATE TRIGGER a BEFORE INSERT OR UPDATE OR DELETE ON bst EXECUTE FUNCTION bst_return()", "CREATE TRIGGER z BEFORE INSERT OR UPDATE OR DELETE ON bst EXECUTE FUNCTION bst_log()")
				q := "INSERT INTO bst(id,v) VALUES($1,$2) RETURNING id,v"
				run(fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}, 1, 10))
				run(fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}}, 2, 20))
				s := triggerRows("UPDATE bst SET v=v+1 RETURNING id,v", []any{int64(1), int64(11)}, []any{int64(2), int64(21)})
				s.Bag = true
				run(s)
				s = triggerRows("DELETE FROM bst RETURNING id,v", []any{int64(1), int64(11)}, []any{int64(2), int64(21)})
				s.Bag = true
				run(s)
				run(beforeStatementLog([]any{"z", "INSERT", int64(0)}, []any{"z", "INSERT", int64(1)}, []any{"z", "UPDATE", int64(2)}, []any{"z", "DELETE", int64(2)}))
			})
		})
	}
	for _, tc := range []struct{ body, code, message, context string }{
		{"RETURN 1;", "42804", "cannot return non-composite value from function returning composite type", "PL/pgSQL function bst_bad_return() line 1 at RETURN"},
		{"", "2F005", "control reached end of trigger procedure without RETURN", "PL/pgSQL function bst_bad_return()"},
		{"RETURN ROW(1/0);", "22012", "division by zero", "PL/pgSQL expression \"ROW(1/0)\"\nPL/pgSQL function bst_bad_return() line 1 at RETURN"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				beforeStatementSetup(t, p, b, c)
				userOperatorExec(t, p, b, c, "CREATE FUNCTION bst_bad_return() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN "+tc.body+" END$$", "CREATE TRIGGER s BEFORE INSERT ON bst EXECUTE FUNCTION bst_bad_return()")
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Context = tc.context
				run(fixture.ErrorDiagnostic("INSERT INTO bst(id,v) VALUES(1,10)", d))
				run(beforeStatementCount("bst", 0))
			})
		})
	}
}
func TestReferenceTriggerBeforeStatementNativeDiagnostics(t *testing.T) {
	for _, fn := range []string{"tsvector_update_trigger", "tsvector_update_trigger_column", "suppress_redundant_updates_trigger"} {
		t.Run(fn, func(t *testing.T) {
			beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				beforeStatementSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO bst(id,v) VALUES(1,10)", 1))
				userOperatorExec(t, p, b, c, "CREATE TRIGGER s BEFORE UPDATE ON bst EXECUTE FUNCTION "+fn+"()")
				code, message := "XX000", "tsvector_update_trigger: must be fired for row"
				if fn == "suppress_redundant_updates_trigger" {
					code = "39P01"
					message = "suppress_redundant_updates_trigger: must be called for each row"
				}
				run(fixture.ErrorDiagnostic("UPDATE bst SET v=v", fixture.ControlsReferenceDiagnostic(code, message)))
				run(triggerRows("SELECT id,v FROM bst", []any{int64(1), int64(10)}))
			})
		})
	}
}
