package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL §10.2 Example 10.4 resolves the unknown operand as the known
// array type. A visible integer overload must not remove that builtin winner.
func TestReferenceUserOperatorPolymorphicUnknownSelection(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "userop_poly")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA userop_poly",
			"SET search_path TO userop_poly,pg_catalog,public")
		for _, phase := range []string{"before_custom", "after_custom"} {
			if phase == "after_custom" {
				userOperatorExec(t, p, b, c,
					"CREATE OPERATOR userop_poly.<@ (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq)")
			}
			t.Run(phase, func(t *testing.T) {
				for _, tc := range []struct {
					query string
					want  bool
				}{
					{"SELECT ARRAY[1,2] <@ '{1,2,3}' AS v", true},
					{"SELECT ARRAY[1,2] <@ '{2,3}' AS v", false},
					{"SELECT ARRAY[1,2] OPERATOR(pg_catalog.<@) '{1,2,3}' AS v", true},
					{"SELECT ARRAY[1,2] OPERATOR(pg_catalog.<@) '{2,3}' AS v", false},
					{"SELECT ARRAY[1,2] <@ ARRAY[1,2,3] AS v", true},
					{"SELECT ARRAY[1,2] <@ ARRAY[2,3] AS v", false},
				} {
					userOperatorValue(t, p, b, c, tc.query, "BOOL", tc.want)
				}

				// No cast on $1: Prepare must infer int4[] from the selected
				// polymorphic signature, then reuse that SAME held statement.
				q := "SELECT ARRAY[1,2] <@ $1 AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct{ input, want any }{
					{"{1,2,3}", true},
					{"{2,3}", false},
					{nil, nil},
					{"{1,2,3}", true},
				} {
					s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.input)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			})
		}
		// These outcomes require the actual custom integer implementation;
		// neither call can be satisfied by the builtin array provider.
		userOperatorValue(t, p, b, c, "SELECT 2 <@ 2 AS v", "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT 2 <@ 3 AS v", "BOOL", false)
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(userop_poly.<@) 2 AS v", "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(userop_poly.<@) 3 AS v", "BOOL", false)
	})
}

// Array concatenation competes with append and prepend: matching each
// polymorphic input independently cannot establish whole-signature viability.
func TestReferenceUserOperatorPolymorphicConsistency(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "userop_concat")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA userop_concat",
			"SET search_path TO userop_concat,pg_catalog,public")
		for _, phase := range []string{"before_custom", "after_custom"} {
			if phase == "after_custom" {
				userOperatorExec(t, p, b, c,
					"CREATE OPERATOR userop_concat.|| (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)")
			}
			t.Run(phase, func(t *testing.T) {
				for _, tc := range []struct{ name, query, want string }{
					{"known_left", "SELECT ARRAY[1,2] || '{3,4}' AS v", "{1,2,3,4}"},
					{"known_right", "SELECT '{1,2}' || ARRAY[3,4] AS v", "{1,2,3,4}"},
					{"typed_concat", "SELECT ARRAY[1,2] || ARRAY[3,4] AS v", "{1,2,3,4}"},
					{"typed_append", "SELECT ARRAY[1,2] || 3::int4 AS v", "{1,2,3}"},
					{"typed_prepend", "SELECT 1::int4 || ARRAY[2,3] AS v", "{1,2,3}"},
				} {
					t.Run(tc.name, func(t *testing.T) {
						s := fixture.ArrayReferenceSelect(tc.query, []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{tc.want}}, b.PG)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					})
				}

				for _, tc := range []struct {
					name, query string
					frames      []struct{ input, want any }
				}{
					{"held_known_left", "SELECT ARRAY[1,2] || $1 AS v", []struct{ input, want any }{
						{"{3,4}", "{1,2,3,4}"},
						{nil, "{1,2}"},
						{"{5,6}", "{1,2,5,6}"},
						{"{3,4}", "{1,2,3,4}"},
					}},
					{"held_known_right", "SELECT $1 || ARRAY[3,4] AS v", []struct{ input, want any }{
						{"{1,2}", "{1,2,3,4}"},
						{nil, "{3,4}"},
						{"{5,6}", "{5,6,3,4}"},
						{"{1,2}", "{1,2,3,4}"},
					}},
				} {
					t.Run(tc.name, func(t *testing.T) {
						// No cast on $1: infer an array at Prepare, then reuse
						// this same statement through NULL and recovery.
						stmt, err := c.PrepareContext(p.Ctx, tc.query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
						for _, frame := range tc.frames {
							s := fixture.ArrayReferenceSelect(tc.query, []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{frame.want}}, b.PG, frame.input)
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						}
					})
				}
			})
		}
		// The custom overload really executes int4pl, rather than merely
		// adding a catalog row to force builtin overload competition.
		userOperatorValue(t, p, b, c, "SELECT 2 || 3 AS v", "INT4", int64(5))
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(userop_concat.||) 3 AS v", "INT4", int64(5))
	})
}
