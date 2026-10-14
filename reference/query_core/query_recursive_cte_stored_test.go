package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryRecursiveCTEStored(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				raw := subqueryRun(t, p, b, c)
				run := func(s fixture.Step) { s.Prepared = prepared; raw(s) }
				raw(fixture.Exec("CREATE TABLE recursive_store_edges(src INT,dst INT)", 0))
				raw(fixture.Exec("INSERT INTO recursive_store_edges VALUES(1,2),(2,3)", 2))
				body := "WITH RECURSIVE r(n) AS (SELECT 1 UNION SELECT e.dst FROM recursive_store_edges e JOIN r ON e.src=r.n) SELECT n FROM r"
				run(fixture.Exec("CREATE VIEW recursive_view AS "+body, 0))
				fixture.ViewCopy(t, p, b, "recursive_view", "recursive_copy")
				run(fixture.Exec("CREATE VIEW recursive_nested AS SELECT n FROM recursive_copy", 0))
				run(fixture.Exec("CREATE VIEW recursive_key_view AS WITH RECURSIVE r(n,depth) AS (SELECT 1,0 UNION SELECT n,depth+1 FROM r WHERE depth<2) SELECT n FROM r", 0))
				keyCheck := func() {
					subqueryBoth(raw, cteBag("SELECT n FROM recursive_key_view", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 1, 1)))
				}
				keyCheck()
				check := func(want [][]any) {
					for _, name := range []string{"recursive_view", "recursive_copy", "recursive_nested"} {
						subqueryBoth(raw, cteBag("SELECT n FROM "+name, []string{"n"}, []string{"INT4"}, want))
					}
				}
				check(recursiveCTEInts(1, 2, 3))
				held := fixture.ViewHold(t, p, b, "SELECT n FROM recursive_view")
				run(fixture.Exec("ALTER TABLE recursive_store_edges RENAME COLUMN dst TO target", 0))
				check(recursiveCTEInts(1, 2, 3))
				raw(fixture.Exec("INSERT INTO recursive_store_edges VALUES(3,4)", 1))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, cteBag("SELECT n FROM recursive_view", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3, 4)))
				check(recursiveCTEInts(1, 2, 3, 4))
				run(fixture.Exec("CREATE MATERIALIZED VIEW recursive_mv AS SELECT n FROM recursive_view", 4))
				raw(fixture.Exec("INSERT INTO recursive_store_edges VALUES(4,5)", 1))
				raw(cteBag("SELECT n FROM recursive_mv", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3, 4)))
				run(fixture.Exec("REFRESH MATERIALIZED VIEW recursive_mv", 0))
				raw(cteBag("SELECT n FROM recursive_mv", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3, 4, 5)))
				run(fixture.Exec("CREATE TABLE recursive_ctas AS "+recursiveCTEFinite, 3))
				run(fixture.Exec("WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT n INTO recursive_into FROM r", 3))
				raw(fixture.Exec("CREATE TABLE recursive_inserted(n INT)", 0))
				run(fixture.Exec("INSERT INTO recursive_inserted "+recursiveCTEFinite, 3))
				for _, name := range []string{"recursive_ctas", "recursive_into", "recursive_inserted"} {
					subqueryBoth(raw, cteBag("SELECT n FROM "+name, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3)))
				}
				raw(fixture.Exec("CREATE SEQUENCE recursive_nodata_seq", 0))
				run(fixture.Exec("CREATE TABLE recursive_nodata AS WITH RECURSIVE r(n) AS (SELECT nextval('recursive_nodata_seq') UNION ALL SELECT nextval('recursive_nodata_seq') FROM r) SELECT n FROM r WITH NO DATA", 0))
				raw(cteBag("SELECT n FROM recursive_nodata", []string{"n"}, []string{"INT8"}, nil))
				raw(fixture.Select("SELECT nextval('recursive_nodata_seq') AS n", []string{"n"}, []string{"INT8"}, recursiveCTEInts(1)))
				raw(fixture.Exec("CREATE TABLE recursive_sentinel(n INT,v INT)", 0))
				raw(fixture.Exec("INSERT INTO recursive_sentinel VALUES(99,99)", 1))
				division := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				run(fixture.ErrorDiagnostic("INSERT INTO recursive_sentinel "+recursiveCTELate+"SELECT n,v FROM r", division))
				raw(cteBag("SELECT * FROM recursive_sentinel", []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
				run(fixture.ErrorDiagnostic("CREATE TABLE recursive_failed AS "+recursiveCTELate+"SELECT n,v FROM r", division))
				missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "recursive_failed" does not exist`)
				missing.Position = "15"
				subqueryAnalysisFailure(raw, "SELECT * FROM recursive_failed", missing)
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DECLARE recursive_cursor CURSOR FOR "+recursiveCTEFinite+" ORDER BY n", 0))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("FETCH ALL FROM recursive_cursor", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3)))
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
					check(recursiveCTEInts(1, 2, 3, 4, 5))
					keyCheck()
					subqueryBoth(raw, cteBag("SELECT n FROM recursive_mv", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3, 4, 5)))
					subqueryBoth(raw, cteBag("SELECT n FROM recursive_inserted", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3)))
					// Restore into a distinct owner through the existing public fixture.
					foreign := fixture.NewScenario(t)
					if err := stuntgres.Restore(foreign.Ctx, foreign.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					for _, fb := range foreign.Backends() {
						if !fb.PG {
							fixture.RunStep(t, foreign.Ctx, fb.DB, fb.Recorder, cteBag("SELECT n FROM recursive_view", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3, 4, 5)))
						}
					}
				}
				// Isolate one dependent so the complete DROP RESTRICT detail is stable.
				raw(fixture.Exec("DROP VIEW recursive_nested", 0))
				raw(fixture.Exec("DROP VIEW recursive_copy", 0))
				raw(fixture.Exec("DROP MATERIALIZED VIEW recursive_mv", 0))
				want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column target of table recursive_store_edges because other objects depend on it")
				want.Detail = "view recursive_view depends on column target of table recursive_store_edges"
				want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				run(fixture.ErrorDiagnostic("ALTER TABLE recursive_store_edges DROP COLUMN target", want))
				subqueryBoth(raw, cteBag("SELECT n FROM recursive_view", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3, 4, 5)))
			})
		})
	}
}
