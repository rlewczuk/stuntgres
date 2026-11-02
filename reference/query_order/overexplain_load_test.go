package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func overexplainInventory(t *testing.T, p *fixture.Pair, c *sql.Conn) []string {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT extname FROM pg_extension ORDER BY extname")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name == "pg_overexplain" {
			t.Fatal("LOAD invented an installed extension")
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

func TestReferenceOverexplainLoad(t *testing.T) {
	for _, alias := range []string{"pg_overexplain", "pg_overexplain.so", "$libdir/pg_overexplain", "$libdir/pg_overexplain.so"} {
		t.Run(alias, func(t *testing.T) {
			overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				overexplainUnloaded(t, p, b, c)
				before := overexplainInventory(t, p, c)
				q := "LOAD '" + alias + "'"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("LOAD Prepare", err)
				}
				defer stmt.Close()
				// Prepare has no activation side effect.
				overexplainUnloaded(t, p, b, c)
				for range 2 {
					overexplainExec(t, p, b, c, q)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
				}
				for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
					r := overexplainQuery(t, p, c, overexplainSQL(format, "DEBUG", "SELECT 1"), format, false)
					overexplainDebug(t, b, r, format, true)
				}
				if after := overexplainInventory(t, p, c); !reflect.DeepEqual(before, after) {
					t.Fatalf("LOAD changed installed inventory: %v -> %v", before, after)
				}
				var notices []fixture.Diagnostic
				_, peer, _ := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				defer peer.Close()
				overexplainUnloaded(t, p, b, peer)
				if !b.PG {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_get_loaded_modules()", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				if len(notices) != 0 {
					t.Fatal(notices)
				}
			})
		})
	}
	t.Run("current-authorization", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"overexplain_reader", "overexplain_super"})
		overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE ROLE overexplain_reader INHERIT", "CREATE ROLE overexplain_super SUPERUSER", "GRANT overexplain_super TO overexplain_reader"} {
				overexplainExec(t, p, b, c, q)
			}
			const q = "LOAD 'pg_overexplain'"
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			overexplainExec(t, p, b, c, "SET ROLE overexplain_reader")
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			denied := fixture.ControlsReferenceDiagnostic("42501", `access to library "pg_overexplain" is not allowed`)
			overexplainError(t, p, b, c, q, denied, "execute", false)
			s := fixture.ErrorDiagnostic(q, denied)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
			overexplainExec(t, p, b, c, "RESET ROLE")
			overexplainExec(t, p, b, c, q)
			overexplainExec(t, p, b, c, "SET ROLE overexplain_reader")
			overexplainError(t, p, b, c, q, denied, "execute", false)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
			r := overexplainQuery(t, p, c, "EXPLAIN (DEBUG) SELECT 1", "TEXT", true)
			overexplainDebug(t, b, r, "TEXT", true)
			overexplainExec(t, p, b, c, "RESET ROLE")
			bad := "LOAD '$bad/pg_overexplain'"
			overexplainError(t, p, b, c, bad, fixture.ControlsReferenceDiagnostic("42602", "invalid macro name in path: $bad/pg_overexplain"), "execute", false)
		})
	})
}
