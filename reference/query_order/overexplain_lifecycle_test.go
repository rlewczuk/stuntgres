package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainLifecycle(t *testing.T) {
	for _, mode := range []string{"rollback", "savepoint", "read-only"} {
		t.Run(mode, func(t *testing.T) {
			overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				overexplainUnloaded(t, p, b, c)
				begin := "BEGIN"
				if mode == "read-only" {
					begin = "BEGIN READ ONLY"
				}
				overexplainExec(t, p, b, c, begin)
				if mode == "savepoint" {
					overexplainExec(t, p, b, c, "SAVEPOINT before_load")
				}
				overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
				if mode == "savepoint" {
					overexplainExec(t, p, b, c, "ROLLBACK TO before_load")
				}
				overexplainExec(t, p, b, c, "ROLLBACK")
				overexplainDebug(t, b, overexplainQuery(t, p, c, "EXPLAIN (DEBUG) SELECT 1", "TEXT", true), "TEXT", true)
				// The protocol handle is closed before DISCARD ALL. The SQL
				// handle remains to prove DISCARD's ordinary deallocation effect.
				overexplainExec(t, p, b, c, "PREPARE overexplain_discard AS SELECT 1")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_prepared_statements WHERE name='overexplain_discard'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
				overexplainExec(t, p, b, c, "DISCARD ALL")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_prepared_statements WHERE name='overexplain_discard'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
					overexplainDebug(t, b, overexplainQuery(t, p, c, overexplainSQL(format, "DEBUG", "SELECT 1"), format, true), format, true)
				}
			})
		})
	}
	t.Run("ordinary-current-role", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"overexplain_lifecycle_reader"})
		overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			overexplainSeed(t, p, b, c)
			for _, q := range []string{"CREATE ROLE overexplain_lifecycle_reader", "GRANT USAGE ON SCHEMA public TO overexplain_lifecycle_reader", "GRANT SELECT ON overexplain_data TO overexplain_lifecycle_reader", "LOAD 'pg_overexplain'", "SET ROLE overexplain_lifecycle_reader"} {
				overexplainExec(t, p, b, c, q)
			}
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			overexplainDebug(t, b, overexplainQuery(t, p, c, "EXPLAIN (DEBUG, FORMAT JSON) SELECT id FROM overexplain_data", "JSON", true), "JSON", true)
			overexplainError(t, p, b, c, "LOAD 'pg_overexplain'", fixture.ControlsReferenceDiagnostic("42501", `access to library "pg_overexplain" is not allowed`), "execute", false)
			overexplainExec(t, p, b, c, "RESET ROLE")
			overexplainInvariant(t, p, b, c)
		})
	})
	for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
		t.Run("held-parameters-"+format, func(t *testing.T) {
			overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				overexplainSeed(t, p, b, c)
				overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
				q := overexplainSQL(format, "DEBUG, VERBOSE, COSTS FALSE", "SELECT $1::integer AS n")
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				var retained overexplainResult
				var snapshot string
				for i, arg := range []any{int64(1), int64(2), nil} {
					rows, err := stmt.QueryContext(p.Ctx, arg)
					if err != nil {
						t.Fatal(err)
					}
					r := overexplainRows(t, rows, format)
					overexplainDebug(t, b, r, format, true)
					if !b.PG && !strings.Contains(r.text, "$1") {
						t.Fatal("native debug evaluated/replaced bound parameter", r.text)
					}
					if b.PG && format != "TEXT" {
						for _, entry := range r.document {
							if entry["PlannedStmt"].(map[string]any)["Executor Parameter Types"] != "none" {
								t.Fatal("external parameter became executor parameter", entry)
							}
						}
					}
					if i == 0 {
						retained, snapshot = r, fmt.Sprint(r)
					}
					s := fixture.Select("SELECT $1::integer AS n", []string{"n"}, []string{"INT4"}, [][]any{{arg}}, arg)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				}
				plan := overexplainQuery(t, p, c, overexplainSQL(format, "DEBUG, VERBOSE", "SELECT id,v FROM overexplain_data ORDER BY id"), format, true)
				planSnapshot := fmt.Sprint(plan)
				overexplainExec(t, p, b, c, "ALTER TABLE overexplain_data RENAME TO overexplain_renamed")
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
				// Close the pool, not just its lease: this ends the physical owner.
				if err := b.DB.Close(); err != nil {
					t.Fatal(err)
				}
				if fmt.Sprint(retained) != snapshot || fmt.Sprint(plan) != planSnapshot {
					t.Fatal("accepted document changed after reexecute/rename/physical close")
				}
				var notices []fixture.Diagnostic
				_, fresh, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				defer fresh.Close()
				b.Recorder = recorder
				overexplainUnloaded(t, p, b, fresh)
				fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Select("SELECT id,v FROM overexplain_renamed ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}))
				if len(notices) != 0 {
					t.Fatal(notices)
				}
			})
		})
	}
	t.Run("native-pre-canceled", func(t *testing.T) {
		overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			if b.PG {
				return
			} // pq pre-canceled publication is not deterministic.
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			result, err := c.ExecContext(ctx, "LOAD 'pg_overexplain'")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled LOAD result=%v err=%v", result, err)
			}
			overexplainUnloaded(t, p, b, c)
			load, err := c.PrepareContext(p.Ctx, "LOAD 'pg_overexplain'")
			if err != nil {
				t.Fatal(err)
			}
			result, err = load.ExecContext(ctx)
			load.Close()
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled held LOAD result=%v err=%v", result, err)
			}
			overexplainUnloaded(t, p, b, c)
			overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
			q := "EXPLAIN (DEBUG, FORMAT JSON) SELECT 1"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, q)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("canceled DEBUG published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			overexplainDebug(t, b, overexplainQuery(t, p, c, q, "JSON", true), "JSON", true)
		})
	})
}
