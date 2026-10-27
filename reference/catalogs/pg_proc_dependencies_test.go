package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Reading retained declaration trees is not current executable admission. Both
// body and default expression retain a checked callee across its replacement.
func TestReferencePGProcDependencies(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer func() {
				if _, err := c.ExecContext(context.Background(), "ALTER FUNCTION pg_catalog.abs(integer) COST 1"); err != nil {
					t.Error(err)
				}
			}()
			pgProcExec(t, p, b, c,
				"CREATE FUNCTION pp_dep_callee(a integer) RETURNS integer LANGUAGE SQL RETURN a+1",
				"CREATE FUNCTION pp_dep_consumer(a integer DEFAULT pp_dep_callee(3)) RETURNS integer LANGUAGE SQL RETURN abs(pp_dep_callee(a))")
			const q = "SELECT proname,prosrc,prosqlbody IS NOT NULL AS body,proargdefaults IS NOT NULL AS defaults,pronargdefaults,pg_get_expr(proargdefaults,0) AS default_sql FROM pg_proc WHERE proname='pp_dep_consumer'"
			const unrelated = "SELECT proname,prosrc FROM pg_proc WHERE oid=1396"
			const parameter = "SELECT parameter_default::text AS default_sql FROM information_schema.parameters WHERE specific_schema='public' AND specific_name LIKE 'pp_dep_consumer_%' AND ordinal_position=1"
			held := make(map[string]fixture.HeldStatement)
			for _, query := range []string{q, unrelated, parameter} {
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held[query] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			}
			defaultSQL := "pp_dep_callee(3)"
			probe := func() {
				t.Helper()
				for _, s := range []fixture.Step{
					fixture.Select(q, []string{"proname", "prosrc", "body", "defaults", "pronargdefaults", "default_sql"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "INT2", "TEXT"}, [][]any{{"pp_dep_consumer", "", true, true, int64(1), defaultSQL}}),
					fixture.Select(unrelated, []string{"proname", "prosrc"}, []string{"NAME", "TEXT"}, [][]any{{"abs", "int8abs"}}),
					fixture.Select(parameter, []string{"default_sql"}, []string{"TEXT"}, [][]any{{defaultSQL}}),
				} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held[s.Query], b.Recorder, s)
				}
			}
			probe()
			const trees = "SELECT prosqlbody,proargdefaults FROM pg_proc WHERE proname='pp_dep_consumer'"
			readTrees := func(rows *sql.Rows) [2]string {
				t.Helper()
				defer rows.Close()
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 2 {
					t.Fatal(types, err)
				}
				for _, typ := range types {
					if typ.DatabaseTypeName() != "PG_NODE_TREE" {
						t.Fatal(typ)
					}
				}
				if !rows.Next() {
					t.Fatal("missing retained declarations", rows.Err())
				}
				var raw [2]any
				if err := rows.Scan(&raw[0], &raw[1]); err != nil {
					t.Fatal(err)
				}
				var text [2]string
				for i, value := range raw {
					if b.PG {
						bytes, ok := value.([]byte)
						if !ok {
							t.Fatalf("PG raw tree %T", value)
						}
						text[i] = string(bytes)
					} else {
						s, ok := value.(string)
						if !ok {
							t.Fatalf("native raw tree %T", value)
						}
						text[i] = s
					}
				}
				if b.PG {
					if !strings.Contains(text[0], "{QUERY") || !strings.Contains(text[0], "{FUNCEXPR") || !strings.Contains(text[1], "{FUNCEXPR") || !strings.Contains(text[1], "{CONST") {
						t.Fatal(text)
					}
				} else if !strings.HasPrefix(text[0], "{RoutineProgram {parser.Select") || !strings.Contains(text[0], "parser.FunctionExpr") || !strings.HasPrefix(text[1], "{ExpressionList ") || !strings.Contains(text[1], "SQLRoutine ") || !strings.Contains(text[1], `Const value "3"`) {
					t.Fatal(text)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("tree multiplicity", rows.Err())
				}
				return text
			}
			initial, err := b.DB.QueryContext(p.Ctx, trees)
			if err != nil {
				t.Fatal(err)
			}
			wantRetained := readTrees(initial)
			old, err := b.DB.QueryContext(p.Ctx, trees)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			pgProcExec(t, p, b, c, "ALTER FUNCTION pg_catalog.abs(integer) COST 7")
			probe()
			pgProcExec(t, p, b, c, "CREATE OR REPLACE FUNCTION pp_dep_callee(a integer) RETURNS integer LANGUAGE SQL RETURN a+2")
			probe()
			pgProcExec(t, p, b, c, "ALTER FUNCTION pp_dep_callee(integer) COST 9 SET application_name='callee_changed'")
			probe()
			pgProcExec(t, p, b, c, "ALTER FUNCTION pp_dep_callee(integer) RENAME TO pp_dep_renamed")
			defaultSQL = "pp_dep_renamed(3)"
			probe()
			if got := readTrees(old); got != wantRetained {
				t.Fatalf("old immutable declarations changed: %q -> %q", wantRetained, got)
			}
			current, err := b.DB.QueryContext(p.Ctx, trees)
			if err != nil {
				t.Fatal(err)
			}
			if got := readTrees(current); got != wantRetained {
				t.Fatalf("metadata recaptured executable callee instead of retained declaration: %q -> %q", wantRetained, got)
			}
		})
	}
}
