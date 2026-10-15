package reference_test

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryCyclePrepared(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cycle_prepared_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			run := subqueryRun(t, p, backend, c)
			cycleSeed(run)
			t.Run("held parameter frames", func(t *testing.T) {
				q := strings.Replace(cycleGraph, "SELECT 'a'::text", "SELECT $1::text", 1) + "SELECT * FROM r WHERE $2::int IS NULL OR cardinality(path)<=$2"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					seed, bound any
					rows        [][]any
				}{
					{"a", nil, cycleGraphRows()},
					{"b", nil, [][]any{{"b", false, "{(b)}"}, {"a", false, "{(b),(a)}"}, {"b", true, "{(b),(a),(b)}"}, {"c", false, "{(b),(a),(c)}"}}},
					{nil, nil, [][]any{{nil, false, "{()}"}}},
					{"a", int64(1), [][]any{{"a", false, "{(a)}"}}},
					{"a", nil, cycleGraphRows()},
				} {
					s := cycleGraphStep(q)
					s.Rows = tc.rows
					s.Args = []any{tc.seed, tc.bound}
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
				}
				s := cycleGraphStep(q)
				s.Args = []any{"a", nil}
				rows, err := stmt.QueryContext(p.Ctx, s.Args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				// Mutate via another physical owner; this cursor retains its
				// statement snapshot, while the next held Execute sees the edge.
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO cycle_edges VALUES('c','d')", 1))
				var got [][]any
				for rows.Next() {
					var n, cyc, path any
					if err := rows.Scan(&n, &cyc, &path); err != nil {
						t.Fatal(err)
					}
					got = append(got, []any{fixture.Normalize(n), fixture.Normalize(cyc), fixture.Normalize(path)})
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				cycleAssertBag(t, got, cycleGraphRows())
				s.Rows = append(cycleGraphRows(), []any{"d", false, "{(a),(c),(d)}"})
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
			})
			for index, projection := range []string{"path", "path::text AS path", "n"} {
				t.Run("field shape "+projection, func(t *testing.T) {
					table := []string{"cycle_shape_raw", "cycle_shape_text", "cycle_shape_scalar"}[index]
					run := subqueryRun(t, p, backend, c)
					run(fixture.Exec("CREATE TABLE "+table+"(n INT)", 0))
					run(fixture.Exec("INSERT INTO "+table+" VALUES(1)", 1))
					q := "WITH RECURSIVE r(n) AS (SELECT n FROM " + table + " UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path SELECT " + projection + " FROM r"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					typ := "_RECORD"
					name := "path"
					want := [][]any{{"{(1)}"}, {"{(1),(1)}"}}
					if index == 1 {
						typ = "TEXT"
					}
					if index == 2 {
						typ = "INT4"
						name = "n"
						want = recursiveCTEInts(1, 1)
					}
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, cteBag(q, []string{name}, []string{typ}, want))
					run(fixture.Exec("ALTER TABLE "+table+" ADD COLUMN ignored INT", 0))
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, cteBag(q, []string{name}, []string{typ}, want))
					run(fixture.Exec("ALTER TABLE "+table+" ALTER COLUMN n TYPE INT", 0))
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, cteBag(q, []string{name}, []string{typ}, want))
					run(fixture.Exec("ALTER TABLE "+table+" ALTER COLUMN n TYPE BIGINT", 0))
					t.Run("held after field type change", func(t *testing.T) {
						if index == 2 {
							s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
							s.SelectRows = true
							s.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
						} else {
							// RECORD[] keeps its public result identity even when
							// its private key-field descriptor changes.
							s := cteBag(q, []string{name}, []string{typ}, want)
							s.CheckMetadata = cycleMetadata(backend, s.Columns, s.Types)
							fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
						}
					})
					t.Run("new statement after field type change", func(t *testing.T) {
						if index == 2 {
							typ = "INT8"
						}
						subqueryBoth(subqueryRun(t, p, backend, c), cteBag(q, []string{name}, []string{typ}, want))
					})
				})
			}
			t.Run("missing source rebind", func(t *testing.T) {
				run := subqueryRun(t, p, backend, c)
				run(fixture.Exec("CREATE TABLE cycle_missing(n INT)", 0))
				q := "WITH RECURSIVE r(n) AS (SELECT n FROM cycle_missing UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path SELECT path FROM r"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				run(fixture.Exec("DROP TABLE cycle_missing", 0))
				d := cycleDiagnostic("42P01", `relation "cycle_missing" does not exist`, q, "cycle_missing")
				s := fixture.ErrorDiagnostic(q, d)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, backend.Recorder, s)
				subqueryAnalysisFailure(run, q, d)
			})
			t.Run("current source authority", func(t *testing.T) {
				run := subqueryRun(t, p, backend, c)
				run(fixture.Exec("CREATE TABLE cycle_acl(n INT)", 0))
				run(fixture.Exec("INSERT INTO cycle_acl VALUES(1)", 1))
				run(fixture.Exec("CREATE ROLE cycle_prepared_reader", 0))
				if backend.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO cycle_prepared_reader", 0))
				}
				run(fixture.Exec("GRANT SELECT ON cycle_acl TO cycle_prepared_reader", 0))
				q := "WITH RECURSIVE r(n) AS (SELECT n FROM cycle_acl UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path SELECT path FROM r"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				run(fixture.Exec("SET SESSION AUTHORIZATION cycle_prepared_reader", 0))
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, cteBag(q, []string{"path"}, []string{"_RECORD"}, [][]any{{"{(1)}"}, {"{(1),(1)}"}}))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("REVOKE SELECT ON cycle_acl FROM cycle_prepared_reader", 0))
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table cycle_acl"))
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
			})
		})
	}
}

// Compare bags independently of the executor's traversal order.
func cycleAssertBag(t *testing.T, got, want [][]any) {
	t.Helper()
	used := make([]bool, len(want))
	for _, row := range got {
		found := false
		for i, candidate := range want {
			if !used[i] && reflect.DeepEqual(row, candidate) {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			t.Errorf("unexpected row %#v; complete bag=%#v want=%#v", row, got, want)
		}
	}
	if len(got) != len(want) {
		t.Errorf("bag cardinality=%d want=%d; got=%#v", len(got), len(want), got)
	}
	for i, seen := range used {
		if !seen {
			t.Errorf("missing row %#v", want[i])
		}
	}
}
