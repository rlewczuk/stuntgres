package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerBeforeStatementQueryOwnership(t *testing.T) {
	t.Run("mode_first_producer", beforeStatementModeOwnership)
	for _, withAfter := range []bool{false, true} {
		name := "before_only"
		if withAfter {
			name = "with_after"
		}
		t.Run(name, func(t *testing.T) {
			beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				beforeStatementSetup(t, p, b, c)
				beforeStatementChain(t, p, b, c, "INSERT OR UPDATE OR DELETE")
				if withAfter {
					userOperatorExec(t, p, b, c, "CREATE FUNCTION bst_after() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$", "CREATE TRIGGER aft AFTER INSERT OR UPDATE ON bst EXECUTE FUNCTION bst_after()")
				}
				q := "WITH x AS (INSERT INTO bst(id,v) VALUES($1,10) RETURNING id,v) INSERT INTO bst(id,v) SELECT id+1,v FROM x"
				run(fixture.Exec(q, 1, 1))
				run(fixture.Exec(q, 1, 3))
				run(beforeStatementLog([]any{"a", "INSERT", int64(0)}, []any{"z", "INSERT", int64(0)}, []any{"a", "INSERT", int64(2)}, []any{"z", "INSERT", int64(2)}))
				userOperatorExec(t, p, b, c, "DROP TRIGGER a ON bst", "DROP TRIGGER z ON bst", "TRUNCATE bst_log", "CREATE TRIGGER only_v BEFORE UPDATE OF v ON bst EXECUTE FUNCTION bst_log()")
				q = "WITH x AS (UPDATE bst SET v=v+1 WHERE id=$1 RETURNING id) UPDATE bst SET u=u+1 WHERE id IN(SELECT id+1 FROM x)"
				run(fixture.Exec(q, 1, 1))
				run(fixture.Exec(q, 1, 3))
				run(beforeStatementCount("bst_log", 0))
				userOperatorExec(t, p, b, c, "CREATE TRIGGER only_u BEFORE UPDATE OF u ON bst EXECUTE FUNCTION bst_log()")
				run(fixture.Exec(q, 1, 1))
				run(fixture.Exec(q, 1, 3))
				run(beforeStatementLog([]any{"only_u", "UPDATE", int64(4)}, []any{"only_u", "UPDATE", int64(4)}))
				userOperatorExec(t, p, b, c, "DROP TRIGGER only_u ON bst", "DROP TRIGGER only_v ON bst", "TRUNCATE bst_log", "CREATE TRIGGER s BEFORE INSERT OR DELETE ON bst EXECUTE FUNCTION bst_log()")
				run(fixture.Exec("WITH x AS (DELETE FROM bst WHERE id=1 RETURNING id,v) INSERT INTO bst(id,v) SELECT id+10,v FROM x", 1))
				s := fixture.Select("SELECT op,count(*) AS n FROM bst_log GROUP BY op ORDER BY op", []string{"op", "n"}, []string{"TEXT", "INT8"}, [][]any{{"DELETE", int64(1)}, {"INSERT", int64(1)}})
				run(s)
				userOperatorExec(t, p, b, c, "TRUNCATE bst_log", "CREATE TABLE bst_guard(id integer)", "CREATE FUNCTION bst_nested() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_log(name,op,n) VALUES('nested',TG_OP,(SELECT count(*) FROM bst)); IF NOT EXISTS(SELECT 1 FROM bst_guard) THEN INSERT INTO bst_guard VALUES(1); INSERT INTO bst(id,v) VALUES(100,100); END IF; RETURN NULL; END$$", "CREATE OR REPLACE TRIGGER s BEFORE INSERT ON bst EXECUTE FUNCTION bst_nested()")
				run(fixture.Exec("INSERT INTO bst(id,v) VALUES($1,10)", 1, 200))
				run(fixture.Exec("INSERT INTO bst(id,v) VALUES($1,10)", 1, 201))
				run(beforeStatementLog([]any{"nested", "INSERT", int64(4)}, []any{"nested", "INSERT", int64(4)}, []any{"nested", "INSERT", int64(6)}))
			})
		})
	}
}
func TestReferenceTriggerBeforeStatementConflictMerge(t *testing.T) {
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		beforeStatementSetup(t, p, b, c)
		beforeStatementChain(t, p, b, c, "INSERT OR UPDATE OR DELETE")
		q := "INSERT INTO bst(id,v) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING id,v"
		run(fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}, 1, 10))
		run(fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(20)}}, 1, 20))
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES(1,30) ON CONFLICT(id) DO UPDATE SET v=excluded.v WHERE false", 0))
		run(fixture.Exec("INSERT INTO bst(id,v) SELECT 2,30 WHERE false ON CONFLICT(id) DO UPDATE SET v=excluded.v", 0))
		var want [][]any
		for i := range 4 {
			n := int64(1)
			if i == 0 {
				n = 0
			}
			for _, op := range []string{"INSERT", "UPDATE"} {
				for _, name := range []string{"a", "z"} {
					want = append(want, []any{name, op, n})
				}
			}
		}
		run(beforeStatementLog(want...))
		userOperatorExec(t, p, b, c, "TRUNCATE bst_log")
		run(fixture.Exec("INSERT INTO bst(id,v) VALUES(1,99) ON CONFLICT DO NOTHING", 0))
		run(beforeStatementLog([]any{"a", "INSERT", int64(1)}, []any{"z", "INSERT", int64(1)}))
		userOperatorExec(t, p, b, c, "TRUNCATE bst_log", "CREATE TABLE bst_source(id integer,v integer)")
		merge := "MERGE INTO bst t USING bst_source s ON t.id=s.id WHEN MATCHED AND s.v<0 THEN DELETE WHEN MATCHED AND s.v>0 THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,s.v)"
		// lib/pq reports MERGE RowsAffected as zero; independently assert effects.
		run(fixture.Exec(merge, 0))
		want = nil
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			for _, name := range []string{"a", "z"} {
				want = append(want, []any{name, op, int64(1)})
			}
		}
		run(beforeStatementLog(want...))
		userOperatorExec(t, p, b, c, "TRUNCATE bst_log")
		run(fixture.Exec("INSERT INTO bst_source VALUES(1,0),(2,22)", 2))
		n := int64(1)
		if b.PG {
			n = 0
		}
		run(fixture.Exec(merge, n))
		run(beforeStatementLog(want...))
		run(triggerRows("SELECT id,v FROM bst ORDER BY id", []any{int64(1), int64(20)}, []any{int64(2), int64(22)}))
		// A strict subset distinguishes declared action events from an
		// unconditional INSERT/UPDATE/DELETE announcement, even with no input.
		userOperatorExec(t, p, b, c, "TRUNCATE bst_log,bst_source")
		updateOnly := "MERGE INTO bst t USING bst_source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v"
		run(fixture.Exec(updateOnly, 0))
		updateAudit := [][]any{{"a", "UPDATE", int64(2)}, {"z", "UPDATE", int64(2)}}
		run(beforeStatementLog(updateAudit...))
		userOperatorExec(t, p, b, c, "TRUNCATE bst_log")
		run(fixture.Exec("INSERT INTO bst_source VALUES(1,21),(2,23)", 2))
		updated := int64(2)
		if b.PG {
			updated = 0
		}
		run(fixture.Exec(updateOnly, updated))
		run(beforeStatementLog(updateAudit...))
		run(triggerRows("SELECT id,v FROM bst ORDER BY id", []any{int64(1), int64(21)}, []any{int64(2), int64(23)}))
	})
}
