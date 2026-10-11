package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryCTEStored(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				raw := subqueryRun(t, p, b, c)
				run := func(s fixture.Step) { s.Prepared = prepared; raw(s) }
				cteFixture(raw)
				run(fixture.Exec("CREATE VIEW cte_view AS WITH a(k,v) AS MATERIALIZED (SELECT id,v FROM t), b AS (SELECT k,v FROM a) SELECT * FROM b", 0))
				run(fixture.Exec("CREATE VIEW cte_reuse AS WITH c AS NOT MATERIALIZED (SELECT id FROM t) SELECT a.id AS first,b.id AS second FROM c a CROSS JOIN c b WHERE a.id=1 AND b.id=2", 0))
				reuse := func(name string) {
					subqueryBoth(raw, cteBag("SELECT * FROM "+name, []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}}))
				}
				check := func() {
					subqueryBoth(raw, cteBag("SELECT * FROM cte_view", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(2), nil}}))
					reuse("cte_reuse")
				}
				check()
				fixture.ViewCopy(t, p, b, "cte_view", "cte_copy")
				fixture.ViewCopy(t, p, b, "cte_reuse", "cte_reuse_copy")
				reuse("cte_reuse_copy")
				run(fixture.Exec("ALTER TABLE t RENAME COLUMN v TO value", 0))
				check()
				want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column value of table t because other objects depend on it")
				want.Detail = "view cte_view depends on column value of table t\nview cte_copy depends on column value of table t"
				want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				// Keep the dependency record deterministic with one stored dependent.
				raw(fixture.Exec("DROP VIEW cte_copy", 0))
				want.Detail = "view cte_view depends on column value of table t"
				raw(fixture.ErrorDiagnostic("ALTER TABLE t DROP COLUMN value", want))
				check()
				run(fixture.Exec("CREATE MATERIALIZED VIEW cte_mv AS WITH c AS (SELECT id,value FROM t) SELECT * FROM c", 3))
				run(fixture.Exec("CREATE TABLE cte_table AS WITH c AS (SELECT id,value FROM t) SELECT * FROM c", 3))
				run(fixture.Exec("WITH c AS (SELECT id,value FROM t) SELECT * INTO cte_into FROM c", 3))
				run(fixture.Exec("CREATE TABLE cte_nodata AS WITH c AS MATERIALIZED (SELECT 1/(id-2) AS n FROM t) SELECT n FROM c WITH NO DATA", 0))
				subqueryBoth(raw, cteBag("SELECT n FROM cte_nodata", []string{"n"}, []string{"INT4"}, nil))
				raw(fixture.Exec("CREATE TABLE inserted(id INT,value INT)", 0))
				run(fixture.Exec("INSERT INTO inserted WITH c AS (SELECT id,value FROM t) SELECT * FROM c", 3))
				for _, name := range []string{"cte_mv", "cte_table", "cte_into", "inserted"} {
					subqueryBoth(raw, cteBag("SELECT * FROM "+name, []string{"id", "value"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(2), nil}}))
				}
				// A failing query consumer must not publish either rows or a table.
				division := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				run(fixture.ErrorDiagnostic("INSERT INTO inserted WITH c AS MATERIALIZED (SELECT 1/(id-2) AS n FROM t) SELECT n,n FROM c", division))
				run(fixture.ErrorDiagnostic("CREATE TABLE cte_failed AS WITH c AS MATERIALIZED (SELECT 1/(id-2) AS n FROM t) SELECT n FROM c", division))
				missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "cte_failed" does not exist`)
				missing.Position = "15"
				raw(fixture.ErrorDiagnostic("SELECT * FROM cte_failed", missing))
				raw(cteBag("SELECT * FROM inserted", []string{"id", "value"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(2), nil}}))
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DECLARE cte_cursor CURSOR FOR WITH c AS MATERIALIZED (SELECT id FROM t) SELECT id FROM c ORDER BY id", 0))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("FETCH ALL FROM cte_cursor", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}}))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					check()
					subqueryBoth(raw, cteBag("SELECT * FROM cte_mv", []string{"id", "value"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(2), nil}}))
				}
			})
		})
	}
}

func TestReferenceQueryCTECatalog(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		catalogs := []string{"pg_class"}
		// pg_type is an oracle-only supplement; native has no pg_type provider.
		if b.PG {
			catalogs = append(catalogs, "pg_type")
		}
		for _, catalog := range catalogs {
			column := map[string]string{"pg_class": "relname", "pg_type": "typname"}[catalog]
			q := "SELECT " + column + " FROM pg_catalog." + catalog + " WHERE " + column + "='cte_transient_probe'"
			subqueryBoth(run, cteBag(q, []string{column}, []string{"NAME"}, nil))
		}
		subqueryBoth(run, cteBag("WITH cte_transient_probe AS (SELECT 42 AS n) SELECT n FROM cte_transient_probe", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
		want := fixture.ControlsReferenceDiagnostic("42P01", `relation "cte_transient_probe" does not exist`)
		want.Position = "15"
		subqueryAnalysisFailure(run, "SELECT * FROM cte_transient_probe", want)
		for _, catalog := range catalogs {
			column := map[string]string{"pg_class": "relname", "pg_type": "typname"}[catalog]
			subqueryBoth(run, cteBag("SELECT "+column+" FROM pg_catalog."+catalog+" WHERE "+column+"='cte_transient_probe'", []string{column}, []string{"NAME"}, nil))
		}
		if !b.PG {
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("transient CTE changed native dump")
			}
		}
	})
}
