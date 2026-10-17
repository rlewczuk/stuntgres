package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQuerySearchPrepared(t *testing.T) {
	searchReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		searchSeed(subqueryRun(t, p, b, c))
		for _, mode := range searchModes {
			t.Run(mode+" fresh held inputs", func(t *testing.T) { searchHeld(t, p, b, c, mode) })
			for _, projection := range []string{"seq", "seq::text AS seq", "n"} {
				t.Run(mode+" shape "+projection, func(t *testing.T) {
					table := "search_shape_" + strings.ToLower(mode)
					if projection == "n" {
						table += "_scalar"
					} else if projection != "seq" {
						table += "_text"
					}
					run := subqueryRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE "+table+"(n INT)", 0))
					run(fixture.Exec("INSERT INTO "+table+" VALUES(1)", 1))
					q := "WITH RECURSIVE r(n) AS (SELECT n FROM " + table + " UNION ALL SELECT n FROM r WHERE FALSE)" + searchClause(mode, "n") + "SELECT " + projection + " FROM r"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					typ := searchType(mode)
					seq := "{(1)}"
					name := "seq"
					var value any = seq
					if mode == "BREADTH" {
						seq = "(0,1)"
						value = seq
					}
					if projection == "n" {
						typ = "INT4"
						name = "n"
						value = int64(1)
					} else if projection != "seq" {
						typ = "TEXT"
					}
					s := fixture.Select(q, []string{name}, []string{typ}, [][]any{{value}})
					s.CheckMetadata = searchMetadata(b, s.Columns, s.Types)
					if projection == "seq" {
						s.CheckRaw = func(t *testing.T, column, row int, v any) bool { searchRawSequence(t, b, v, seq); return true }
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					run(fixture.Exec("ALTER TABLE "+table+" ADD COLUMN ignored INT", 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					run(fixture.Exec("ALTER TABLE "+table+" ALTER COLUMN n TYPE BIGINT", 0))
					// Both public pseudo-type identities remain stable when a nested
					// key changes type; an exposed scalar result does not.
					if projection == "n" {
						rows, err := stmt.QueryContext(p.Ctx)
						if rows != nil {
							rows.Close()
							t.Error("failed held query published Rows")
						}
						want := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type")
						fixture.AssertDiagnostic(t, err, &want)
					} else {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
					if projection == "n" {
						s.Types = []string{"INT8"}
						s.CheckMetadata = searchMetadata(b, s.Columns, s.Types)
					}
					subqueryBoth(run, s)
				})
			}
		}
	})
}

func searchHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mode string) {
	t.Helper()
	q := "WITH RECURSIVE r(n) AS (SELECT $1::int UNION ALL SELECT n+1 FROM r WHERE n<$2::int)" + searchClause(mode, "n") + "SELECT n,seq FROM r ORDER BY seq"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		seed, bound any
		dfs, bfs    [][]any
	}{
		{int64(1), int64(2), [][]any{{int64(1), "{(1)}"}, {int64(2), "{(1),(2)}"}}, [][]any{{int64(1), "(0,1)"}, {int64(2), "(1,2)"}}},
		{int64(10), int64(10), [][]any{{int64(10), "{(10)}"}}, [][]any{{int64(10), "(0,10)"}}},
		{nil, int64(3), [][]any{{nil, "{()}"}}, [][]any{{nil, "(0,)"}}},
		{int64(1), int64(2), [][]any{{int64(1), "{(1)}"}, {int64(2), "{(1),(2)}"}}, [][]any{{int64(1), "(0,1)"}, {int64(2), "(1,2)"}}},
	} {
		want := tc.dfs
		if mode == "BREADTH" {
			want = tc.bfs
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"n", "seq"}, []string{"INT4", searchType(mode)}, want, tc.seed, tc.bound))
	}
	q = searchTree(mode) + "SELECT id,seq FROM r ORDER BY seq"
	stmt2, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt2.Close()
	held2 := fixture.HeldStatement{Executor: c, Statement: stmt2, SQL: q}
	s := searchTreeStep(mode, q)
	fixture.RunStep(t, p.Ctx, held2, b.Recorder, s)
	run := subqueryRun(t, p, b, c)
	run(fixture.Exec("INSERT INTO search_nodes VALUES(12,11)", 1))
	s.Rows = append(searchTreeRows(mode), []any{int64(12), "{(1),(10),(11),(12)}"})
	if mode == "BREADTH" {
		s.Rows = append(searchTreeRows(mode), []any{int64(12), "(3,12)"})
	}
	fixture.RunStep(t, p.Ctx, held2, b.Recorder, s)
	run(fixture.Exec("DELETE FROM search_nodes WHERE id=12", 1))
	fixture.RunStep(t, p.Ctx, held2, b.Recorder, searchTreeStep(mode, q))
}
