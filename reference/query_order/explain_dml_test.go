package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainDML(t *testing.T) {
	t.Run("row-security", explainDMLRowSecurity)
	t.Run("current-of", explainDMLCursor)
	t.Run("source-returning", explainDMLSourceReturning)
	cases := []struct {
		name, sql string
		details   []string
	}{
		{"insert-values", "INSERT INTO explain_dml(id,v) VALUES(4,40),(5,50)", []string{"Insert", "Values", "id", "v"}},
		{"insert-default", "INSERT INTO explain_dml DEFAULT VALUES", []string{"Insert", "DEFAULT", "nextval"}},
		{"insert-query", "INSERT INTO explain_dml(id,v) SELECT id+10,v FROM explain_dml WHERE id>1 RETURNING id", []string{"Insert", "Project", "Filter", "RETURNING", "+"}},
		{"conflict-nothing", "INSERT INTO explain_dml(id,v) VALUES(1,99) ON CONFLICT(id) DO NOTHING", []string{"Insert", "Conflict", "NOTHING", "id"}},
		{"conflict-update", "INSERT INTO explain_dml(id,v) VALUES(1,99) ON CONFLICT(id) DO UPDATE SET v=excluded.v WHERE excluded.v>explain_dml.v RETURNING id,v", []string{"Insert", "Conflict", "UPDATE", "excluded.v", ">", "RETURNING"}},
		{"update", "UPDATE explain_dml SET v=v+1 WHERE id=1", []string{"Update", "v", "+", "id", "Filter"}},
		{"update-images", "UPDATE explain_dml SET v=99 WHERE id=1 RETURNING old.v,v", []string{"Update", "old.v", "explain_dml.v", "RETURNING"}},
		{"update-default", "UPDATE explain_default_target SET v=DEFAULT WHERE v=41 RETURNING v", []string{"Update", "explain_default_target", "nextval", "RETURNING"}},
		{"update-from", "UPDATE explain_dml t SET v=s.v FROM explain_source s WHERE t.id=s.id RETURNING t.id", []string{"Update", "explain_source", "s.v", "t.id", "RETURNING"}},
		{"update-group", "UPDATE explain_dml SET (id,v)=(SELECT id,v+1 FROM explain_source WHERE id=1) WHERE id=1", []string{"Update", "SubPlan", "explain_source", "id", "v"}},
		{"update-array", "UPDATE explain_dml SET a[1]=9 WHERE id=1 RETURNING a", []string{"Update", "a", "9", "RETURNING"}},
		{"delete", "DELETE FROM explain_dml WHERE id=1", []string{"Delete", "Filter", "id"}},
		{"delete-using", "DELETE FROM explain_dml t USING explain_source s WHERE t.id=s.id RETURNING t.id", []string{"Delete", "explain_source", "t.id", "s.id", "RETURNING"}},
		{"merge", "MERGE INTO explain_dml t USING explain_source s ON t.id=s.id WHEN MATCHED AND s.v>0 THEN UPDATE SET v=s.v WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY TARGET AND s.id>0 THEN INSERT(id,v) VALUES(s.id,s.v) WHEN NOT MATCHED BY SOURCE AND t.id>0 THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DO NOTHING RETURNING t.id", []string{"Merge", "explain_source", "t.id", "s.id", "MATCHED", "SOURCE", "TARGET", "UPDATE", "DELETE", "INSERT", "NOTHING", "RETURNING"}},
		{"merge-no-returning", "MERGE INTO explain_dml t USING explain_source s ON t.id=s.id WHEN MATCHED THEN DO NOTHING WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,s.v)", []string{"Merge", "NOTHING", "INSERT", "explain_source"}},
		{"modifying-with", "WITH changed AS (UPDATE explain_dml SET v=99 WHERE id=1 RETURNING id) DELETE FROM explain_dml WHERE id IN(SELECT id FROM changed) RETURNING id", []string{"Delete", "Update", "changed", "99", "RETURNING"}},
		{"insert-with", "WITH changed AS (DELETE FROM explain_source RETURNING id,v) INSERT INTO explain_dml(id,v) SELECT id+10,v FROM changed", []string{"Insert", "Delete", "changed", "explain_source"}},
		{"view-update", "UPDATE explain_write_view SET v=88 WHERE id=1 RETURNING id", []string{"Update", "explain_dml", "88", "RETURNING"}},
		{"rule-insert", "INSERT INTO explain_rule VALUES(8)", []string{"Insert", "explain_log", "8"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, q := range []string{
						"CREATE SEQUENCE explain_dml_s START 100",
						"CREATE TABLE explain_dml(id integer PRIMARY KEY DEFAULT nextval('explain_dml_s'),v integer DEFAULT 7,a integer[] DEFAULT ARRAY[1,2])",
						"CREATE TABLE explain_source(id integer,v integer)",
						"CREATE VIEW explain_write_view AS SELECT id,v FROM explain_dml",
						"CREATE TABLE explain_rule(id integer)",
						"CREATE TABLE explain_log(id integer)",
						"CREATE RULE explain_copy AS ON INSERT TO explain_rule DO ALSO INSERT INTO explain_log VALUES(NEW.id)",
					} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO explain_dml(id,v) VALUES(1,10),(2,20),(3,30)", 3))
					run(fixture.Exec("INSERT INTO explain_source VALUES(1,11),(4,44)", 2))
					if c.name == "update-default" {
						run(fixture.Exec("CREATE DOMAIN explain_default_value AS integer DEFAULT nextval('explain_dml_s')", 0))
						run(fixture.Exec("CREATE TABLE explain_default_target(v explain_default_value)", 0))
						run(fixture.Exec("INSERT INTO explain_default_target VALUES(41)", 1))
					}
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
							lines := explainQuery(t, p, b, "EXPLAIN (VERBOSE) "+c.sql, prepared)
							explainCosts(t, b, lines, true)
							if !b.PG && !strings.Contains(c.sql, "RETURNING") {
								explainContains(t, lines[:1], "estimated rows=0")
							}
							if b.PG {
								// PostgreSQL may replace source stages or simplify conditions;
								// only command target identity is fixed, never its physical choice.
								target := "explain_dml"
								if c.name == "rule-insert" {
									target = "explain_log"
								}
								if c.name == "update-default" {
									target = "explain_default_target"
								}
								explainContains(t, lines, target)
							} else {
								explainContains(t, lines, c.details...)
							}
							run(fixture.Select("SELECT id,v,a[1] AS first FROM explain_dml ORDER BY id", []string{"id", "v", "first"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(1)}, {int64(2), int64(20), int64(1)}, {int64(3), int64(30), int64(1)}}))
							run(fixture.Select("SELECT id,v FROM explain_source ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(4), int64(44)}}))
							run(fixture.Select("SELECT id FROM explain_rule ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
							run(fixture.Select("SELECT id FROM explain_log ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
							if c.name == "update-default" {
								run(fixture.Select("SELECT v::integer AS v FROM explain_default_target ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
							}
						})
					}
					run(fixture.Select("SELECT nextval('explain_dml_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(100)}}))
					run(fixture.Exec("UPDATE explain_dml SET v=21 WHERE id=2", 1))
					run(fixture.Select("SELECT v FROM explain_dml WHERE id=2", []string{"v"}, []string{"INT4"}, [][]any{{int64(21)}}))
				})
			}
		})
	}
}

func explainDMLRowSecurity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"explain_policy_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{
				"CREATE ROLE explain_policy_reader",
				"GRANT USAGE ON SCHEMA public TO explain_policy_reader",
				"CREATE TABLE explain_policy(id integer,v integer)",
				"GRANT SELECT,UPDATE,DELETE ON explain_policy TO explain_policy_reader",
				"ALTER TABLE explain_policy ENABLE ROW LEVEL SECURITY",
				"CREATE POLICY visible ON explain_policy USING(id=1) WITH CHECK(TRUE)",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO explain_policy VALUES(1,10),(2,20)", 2))
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION explain_policy_reader"); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			for _, q := range []string{
				"EXPLAIN (VERBOSE) UPDATE explain_policy SET v=99 RETURNING id",
				"EXPLAIN (VERBOSE) DELETE FROM explain_policy RETURNING id",
			} {
				for _, prepared := range []bool{false, true} {
					var lines []string
					if prepared {
						stmt, err := conn.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						rows, err := stmt.QueryContext(p.Ctx)
						if err != nil {
							stmt.Close()
							t.Fatal(err)
						}
						lines = explainTextRows(t, rows)
						stmt.Close()
					} else {
						rows, err := conn.QueryContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						lines = explainTextRows(t, rows)
					}
					explainContains(t, lines, "explain_policy", "id", "1")
					if !b.PG {
						explainContains(t, lines, "Filter", "RETURNING")
					}
					run(fixture.Select("SELECT id,v FROM explain_policy ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
				}
			}
		})
	}
}

func explainDMLCursor(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE explain_cursor_t(id integer,v integer)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO explain_cursor_t VALUES(1,10),(2,20)", 2))
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DECLARE explain_current CURSOR FOR SELECT id,v FROM explain_cursor_t ORDER BY id FOR UPDATE", 0))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("FETCH NEXT FROM explain_current", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
			for _, q := range []string{
				"EXPLAIN UPDATE explain_cursor_t SET v=99 WHERE CURRENT OF explain_current",
				"EXPLAIN DELETE FROM explain_cursor_t WHERE CURRENT OF explain_current",
				"EXPLAIN UPDATE explain_cursor_t SET v=99 WHERE CURRENT OF explain_current RETURNING v",
				"EXPLAIN DELETE FROM explain_cursor_t WHERE CURRENT OF explain_current RETURNING id",
			} {
				for _, prepared := range []bool{false, true} {
					lines := explainTextRows(t, mustExplainCapabilityRows(t, p, tx, q, prepared))
					explainContains(t, lines, "explain_cursor_t")
					if !b.PG {
						explainContains(t, lines, "CURRENT OF", "explain_current", "Cursor Locator", "estimated rows=1", "logical cost=1.02")
						if strings.Contains(strings.Join(lines, "\n"), "Logical Scan") {
							t.Fatal("cursor locator fabricated a target scan")
						}
					}
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT id,v FROM explain_cursor_t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
				}
			}
			// Explanation must not advance the positioned cursor.
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("FETCH NEXT FROM explain_current", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}}))
		})
	}
}
