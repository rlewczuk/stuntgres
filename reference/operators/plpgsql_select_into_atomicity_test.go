package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSelectIntoAtomicity(t *testing.T) {
	t.Run("sequential_local_publication", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE si_atomic_row(a integer,b integer)",
				"CREATE FUNCTION si_partial() RETURNS text LANGUAGE plpgsql AS $$DECLARE a integer:=10; b integer:=20; f boolean; BEGIN BEGIN SELECT 7,'bad'::text INTO a,b; EXCEPTION WHEN invalid_text_representation THEN f:=FOUND; END; RETURN a||':'||b||':'||f; END$$",
				"CREATE FUNCTION si_partial_fields() RETURNS text LANGUAGE plpgsql AS $$DECLARE r si_atomic_row%ROWTYPE; f boolean; BEGIN r:=ROW(10,20); BEGIN SELECT 7,'bad'::text INTO r.a,r.b; EXCEPTION WHEN invalid_text_representation THEN f:=FOUND; END; RETURN r.a||':'||r.b||':'||f; END$$",
				"CREATE FUNCTION si_snapshot() RETURNS text LANGUAGE plpgsql AS $$DECLARE a integer:=10; b integer:=20; BEGIN SELECT b,a INTO a,b; RETURN a||':'||b; END$$")
			plpgsqlValue(t, p, b, c, "SELECT si_partial() AS v", "TEXT", "7:20:true")
			plpgsqlValue(t, p, b, c, "SELECT si_partial_fields() AS v", "TEXT", "7:20:true")
			plpgsqlValue(t, p, b, c, "SELECT si_snapshot() AS v", "TEXT", "20:10")
		})
	})
	for _, producer := range []string{"insert", "update", "delete"} {
		t.Run("protected_"+producer, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				query := "INSERT INTO si_atomic_data SELECT id+10 FROM si_atomic_data RETURNING id INTO x"
				if producer == "update" {
					query = "UPDATE si_atomic_data SET id=id+10 RETURNING id INTO x"
				}
				if producer == "delete" {
					query = "DELETE FROM si_atomic_data RETURNING id INTO x"
				}
				userOperatorExec(t, p, b, c, "CREATE TABLE si_atomic_data(id integer)", "CREATE TABLE si_atomic_log(id integer)",
					"CREATE FUNCTION si_atomic() RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=99; log_id integer; f boolean; BEGIN INSERT INTO si_atomic_log VALUES(8) RETURNING id INTO log_id; BEGIN INSERT INTO si_atomic_log VALUES(9) RETURNING id INTO log_id; "+query+"; EXCEPTION WHEN too_many_rows THEN f:=FOUND; END; RETURN x||':'||f; END$$")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO si_atomic_data VALUES(1),(2),(3)", 3))
				for _, prepared := range []bool{false, true} {
					previousRows := int64(0)
					if prepared {
						previousRows = 1
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM si_atomic_log", previousRows))
					s := fixture.Select("SELECT si_atomic() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"99:true"}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_atomic_data ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_atomic_log ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
				}
			})
		})
	}
	t.Run("uncaught_and_same_held_savepoint", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE si_tx_data(id integer)", "CREATE TABLE si_tx_log(id integer)",
				"CREATE FUNCTION si_tx(n integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; y integer; BEGIN INSERT INTO si_tx_log VALUES(9) RETURNING id INTO y; UPDATE si_tx_data SET id=id+10 WHERE id<=n RETURNING id INTO x; RETURN x; END$$")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO si_tx_data VALUES(1),(2)", 2))
			q := "SELECT si_tx($1) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			want := plpgsqlRuntimeDiagnostic("P0003", "query returned more than one row", "si_tx(integer)", "SQL statement")
			want.Hint = "Make sure the query returns a single row, or use LIMIT 1."
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, want, 2)
				s.SelectRows, s.ErrorStage, s.Prepared = true, "execute", prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_tx_log", "INT8", int64(0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_tx_data ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO si_tx_log VALUES(8)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SAVEPOINT before_capture", 0))
			s := fixture.ErrorDiagnostic(q, want, 2)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK TO SAVEPOINT before_capture", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}, 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("COMMIT", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_tx_log ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(9)}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_tx_data ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(11)}}))
		})
	})
}
