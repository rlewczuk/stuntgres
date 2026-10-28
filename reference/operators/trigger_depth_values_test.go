package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func depthZero() fixture.Step {
	return fixture.Select("SELECT pg_trigger_depth()", []string{"pg_trigger_depth"}, []string{"INT4"}, [][]any{{int64(0)}})
}

func depthHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) *sql.Stmt {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func depthReadHeld(t *testing.T, p *fixture.Pair, s *sql.Stmt) {
	t.Helper()
	var v any
	if err := s.QueryRowContext(p.Ctx).Scan(&v); err != nil || v != int64(0) {
		t.Fatalf("held depth %#v: %v", v, err)
	}
}

func TestReferenceTriggerDepthValues(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		held := depthHeld(t, p, c, "SELECT pg_trigger_depth()")
		for range 2 {
			run(depthZero())
			depthReadHeld(t, p, held)
			run(fixture.Prepared(depthZero()))
		}
		for _, q := range []string{"SELECT pg_catalog.pg_trigger_depth() AS pg_trigger_depth", "SELECT * FROM pg_trigger_depth()"} {
			run(fixture.Select(q, []string{"pg_trigger_depth"}, []string{"INT4"}, [][]any{{int64(0)}}))
		}
		run(fixture.Select("SELECT pg_typeof(pg_trigger_depth())::text AS typ", []string{"typ"}, []string{"TEXT"}, [][]any{{"integer"}}))
		for _, empty := range []bool{false, true} {
			q := "SELECT pg_trigger_depth()"
			if empty {
				q += " WHERE false"
			}
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if prepared {
					s := depthHeld(t, p, c, q)
					rows, err = s.QueryContext(p.Ctx)
				} else {
					rows, err = c.QueryContext(p.Ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(cols) != 1 || cols[0].Name() != "pg_trigger_depth" || cols[0].DatabaseTypeName() != "INT4" {
					t.Fatalf("metadata %v", cols)
				}
				want := reflect.TypeFor[int64]()
				if b.PG {
					want = reflect.TypeFor[int32]()
				}
				if cols[0].ScanType() != want {
					t.Fatalf("scan type %v want %v", cols[0].ScanType(), want)
				}
				if nullable, known := cols[0].Nullable(); nullable || known {
					t.Fatalf("nullability %v %v", nullable, known)
				}
				if !empty {
					if !rows.Next() {
						t.Fatal("missing depth", rows.Err())
					}
					var v any
					if err := rows.Scan(&v); err != nil || v != int64(0) {
						t.Fatalf("depth %#v %v", v, err)
					}
				}
				if rows.Next() || rows.Err() != nil || rows.NextResultSet() {
					t.Fatal("extra results", rows.Err())
				}
				rows.Close()
			}
		}
		userOperatorExec(t, p, b, c, "CREATE FUNCTION public.pg_trigger_depth() RETURNS integer LANGUAGE SQL AS 'SELECT 73'", "SET search_path=public,pg_catalog")
		run(fixture.Select("SELECT pg_trigger_depth()", []string{"pg_trigger_depth"}, []string{"INT4"}, [][]any{{int64(73)}}))
		run(fixture.Select("SELECT pg_catalog.pg_trigger_depth() AS d", []string{"d"}, []string{"INT4"}, [][]any{{int64(0)}}))
		userOperatorExec(t, p, b, c, "RESET search_path")
	})
}
