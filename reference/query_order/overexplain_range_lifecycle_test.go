package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainRangeLifecycle(t *testing.T) {
	t.Run("ordinary-nonexecution-and-returning-owners", func(t *testing.T) {
		overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			overexplainRangeSeed(t, p, b, c)
			for _, q := range []string{"CREATE TABLE range_default(id integer DEFAULT nextval('range_seq'))", "CREATE TABLE range_input(id integer,v text)", "ALTER TABLE range_src ADD PRIMARY KEY(id)", "LOAD 'pg_overexplain'"} {
				overexplainExec(t, p, b, c, q)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO range_input VALUES(1,'input'),(4,NULL)", 2))
			cases := []struct {
				child, target string
				source        bool
			}{
				{"INSERT INTO range_default DEFAULT VALUES RETURNING id", "range_default", false},
				{"SELECT nextval('range_seq'),id FROM range_src", "", false},
				{"SELECT 1/(id-id) FROM range_src", "", false},
				{"WITH changed AS (UPDATE range_src SET v='changed' RETURNING id) SELECT id FROM changed", "range_src", false},
				{"INSERT INTO range_src AS t SELECT s.id,s.v FROM range_input s RETURNING t.id", "t", true},
				{"INSERT INTO range_src AS t VALUES(1,'changed') ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING t.id", "t", false},
				{"UPDATE range_src t SET v=s.v FROM range_input s WHERE t.id=s.id RETURNING t.id,s.v,(SELECT s.v)", "t", true},
				{"DELETE FROM range_src t USING range_input s WHERE t.id=s.id RETURNING t.id,s.v,(SELECT s.v)", "t", true},
			}
			for _, tc := range cases {
				q := overexplainSQL("JSON", "RANGE_TABLE, VERBOSE, COSTS FALSE", tc.child)
				for _, prepared := range []bool{false, true} {
					var stmt *sql.Stmt
					var err error
					if prepared {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
					}
					for range 2 {
						var r overexplainResult
						if prepared {
							rows, err := stmt.QueryContext(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							r = overexplainRows(t, rows, "JSON")
						} else {
							r = overexplainQuery(t, p, c, q, "JSON", false)
						}
						entries := overexplainRange(t, b, r, "JSON", true)
						if tc.target != "" {
							e := overexplainRangeNamed(t, b, entries, "relation", tc.target)
							if !b.PG && e["Role"] != "target" && e["Role"] != "target/source" {
								t.Fatal("lost analyzed mutation target", e)
							}
						}
						if tc.source {
							overexplainRangeNamed(t, b, entries, "relation", "s")
						}
						if !b.PG {
							for _, e := range entries {
								if e["Range Name"] == "excluded" && e["Relation OID"] != nil {
									t.Fatal("excluded fabricated a physical relation", e)
								}
							}
						}
					}
					if stmt != nil {
						stmt.Close()
					}
				}
				overexplainRangeValues(t, p, b, c)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM range_default ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('range_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			// A real invocation still evaluates the erroring expression.
			overexplainError(t, p, b, c, "SELECT 1/(id-id) FROM range_src", fixture.ControlsReferenceDiagnostic("22012", "division by zero"), "execute", true)
		})
	})
	for _, mode := range []string{"rollback", "savepoint", "read-only"} {
		t.Run("activation-"+mode, func(t *testing.T) {
			overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				overexplainRangeUnloaded(t, p, b, c)
				begin := "BEGIN"
				if mode == "read-only" {
					begin = "BEGIN READ ONLY"
				}
				overexplainExec(t, p, b, c, begin)
				if mode == "savepoint" {
					overexplainExec(t, p, b, c, "SAVEPOINT before_range_load")
				}
				overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
				if mode == "savepoint" {
					overexplainExec(t, p, b, c, "ROLLBACK TO before_range_load")
				}
				overexplainExec(t, p, b, c, "ROLLBACK")
				overexplainExec(t, p, b, c, "PREPARE range_discard AS SELECT 1")
				overexplainExec(t, p, b, c, "DISCARD ALL")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_prepared_statements WHERE name='range_discard'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
					overexplainRange(t, b, overexplainQuery(t, p, c, overexplainSQL(format, "RANGE_TABLE", "SELECT 1"), format, true), format, true)
				}
			})
		})
	}
	t.Run("current-catalog-and-physical-end", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "range_before")
		fixture.SchemaReferenceCleanup(t, "range_after")
		overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE SCHEMA range_before", "CREATE TABLE range_before.range_life(id integer,v text)", "SET search_path=range_before,public", "LOAD 'pg_overexplain'"} {
				overexplainExec(t, p, b, c, q)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO range_life VALUES(1,NULL)", 1))
			q := overexplainSQL("JSON", "RANGE_TABLE, VERBOSE", "SELECT id,v FROM range_life")
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			executor := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			retained := overexplainQuery(t, p, executor, q, "JSON", false)
			snapshot := fmt.Sprint(retained)
			before := overexplainRangeNamed(t, b, overexplainRange(t, b, retained, "JSON", true), "relation", "range_life")
			overexplainExec(t, p, b, c, "ALTER SCHEMA range_before RENAME TO range_after")
			overexplainExec(t, p, b, c, "SET search_path=range_after,public")
			r := overexplainQuery(t, p, executor, q, "JSON", false)
			after := overexplainRangeNamed(t, b, overexplainRange(t, b, r, "JSON", true), "relation", "range_life")
			if b.PG {
				if after["Relation"] != "range_after.range_life" {
					t.Fatal("held PG stale namespace", after)
				}
			} else {
				overexplainRangeRelation(t, p, c, after, "range_after.range_life", "JSON")
				if before["Relation OID"] != after["Relation OID"] {
					t.Fatal("rename replaced actual identity", before, after)
				}
			}
			overexplainExec(t, p, b, c, "DROP TABLE range_life")
			overexplainExec(t, p, b, c, "CREATE TABLE range_life(id integer,v text)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO range_life VALUES(2,'new')", 1))
			r = overexplainQuery(t, p, executor, q, "JSON", false)
			after = overexplainRangeNamed(t, b, overexplainRange(t, b, r, "JSON", true), "relation", "range_life")
			if !b.PG {
				overexplainRangeRelation(t, p, c, after, "range_after.range_life", "JSON")
				if before["Relation OID"] == after["Relation OID"] {
					t.Fatal("held definition retained dropped identity", before, after)
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Select("SELECT id,v FROM range_life ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "new"}})))
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if err := b.DB.Close(); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(retained) != snapshot {
				t.Fatal("retained document changed after replacement/physical teardown")
			}
			var notices []fixture.Diagnostic
			_, fresh, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			defer fresh.Close()
			b.Recorder = recorder
			overexplainRangeUnloaded(t, p, b, fresh)
			if len(notices) != 0 {
				t.Fatal(notices)
			}
		})
	})
	t.Run("native-pre-canceled", func(t *testing.T) {
		overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			if b.PG {
				return
			} // pq cancellation publication is a different adapter contract.
			overexplainRangeSeed(t, p, b, c)
			overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
			q := overexplainSQL("JSON", "RANGE_TABLE, ANALYZE", "UPDATE range_src SET id=99")
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, q)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("canceled RANGE_TABLE published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary context cancellation fabricated SQLSTATE", state.SQLState())
				}
			}
			overexplainRangeValues(t, p, b, c)
		})
	})
}
