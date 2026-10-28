package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayInspectReviewGrouping(t *testing.T) {
	arrayInspectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ai_review_group(a INT[])", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ai_review_group VALUES ('[0:1]={1,NULL}'),('[0:1]={2,3}')", 2))
		for _, call := range []struct {
			name, tail, typ string
			want            any
		}{
			{"array_dims", "", "TEXT", "[0:1]"},
			{"array_length", ",1", "INT4", int64(2)},
			{"array_lower", ",1", "INT4", int64(0)},
			{"array_upper", ",1", "INT4", int64(1)},
			{"array_ndims", "", "INT4", int64(1)},
			{"cardinality", "", "INT4", int64(2)},
		} {
			for _, qualifiedProjection := range []bool{false, true} {
				projection, grouping := call.name, "pg_catalog."+call.name
				if qualifiedProjection {
					projection, grouping = grouping, projection
				}
				t.Run(projection+"_group_by_"+grouping, func(t *testing.T) {
					q := "SELECT " + projection + "(a" + call.tail + ") AS v,count(*) AS copies FROM ai_review_group GROUP BY " + grouping + "(a" + call.tail + ")"
					arrayInspectBoth(t, p, b, c, fixture.Select(q, []string{"v", "copies"}, []string{call.typ, "INT8"}, [][]any{{call.want, int64(2)}}))
				})
			}
		}
	})
}

func TestReferenceArrayInspectReviewLabels(t *testing.T) {
	arrayInspectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ call, token, code, message string }{
			{"array_length(a => ARRAY[1], 1)", "1)", "42601", "positional argument cannot follow named argument"},
			{"array_length(a => ARRAY[1], a => 1)", "a => 1", "42601", `argument name "a" used more than once`},
			{"array_lower(a => ARRAY[1], 1)", "1)", "42601", "positional argument cannot follow named argument"},
			{"array_upper(a => ARRAY[1], a => 1)", "a => 1", "42601", `argument name "a" used more than once`},
			{"array_length(a => missing, 1)", "missing", "42703", `column "missing" does not exist`},
			{"array_length(a => ARRAY[1], a => missing)", "missing", "42703", `column "missing" does not exist`},
			{"array_length(a => ARRAY[1], 1) FILTER(WHERE missing)", "missing", "42703", `column "missing" does not exist`},
			{"array_length(a => ARRAY[1], a => 1) FILTER(WHERE missing)", "missing", "42703", `column "missing" does not exist`},
		} {
			t.Run(tc.call, func(t *testing.T) {
				for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
					q := prefix + tc.call
					arrayInspectDiagnosticBoth(t, p, b, c, q, arrayInspectDiagnostic(q, tc.token, tc.code, tc.message, ""))
				}
			})
		}
	})
}

func TestReferenceArrayInspectReviewNamespaces(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ai_review_reader"})
	arrayInspectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE ai_review_reader", 0))
		run(fixture.Exec("CREATE SCHEMA ai_review_private", 0))
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA ai_review_private CASCADE"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(err)
				}
			}
			if !b.PG {
				if _, err := c.ExecContext(p.Ctx, "ALTER SCHEMA public OWNER TO CURRENT_USER"); err != nil {
					t.Error(err)
				}
			}
		}()
		// Establish real namespace visibility without inventing native schema grants.
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ai_review_reader", 0))
		} else {
			run(fixture.Exec("ALTER SCHEMA public OWNER TO ai_review_reader", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION ai_review_reader", 0))
		for _, schema := range []string{"ai_review_absent", "ai_review_private"} {
			for _, name := range []string{"array_dims", "array_length", "array_lower", "array_upper", "array_ndims", "cardinality"} {
				tail := ""
				if name == "array_length" || name == "array_lower" || name == "array_upper" {
					tail = ",1"
				}
				for _, call := range []string{
					schema + "." + name + "(missing" + tail + ")",
					schema + "." + name + "(ARRAY[1]" + tail + ") FILTER(WHERE missing)",
				} {
					t.Run(call, func(t *testing.T) {
						for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
							q := prefix + call
							arrayInspectDiagnosticBoth(t, p, b, c, q, arrayInspectDiagnostic(q, "missing", "42703", `column "missing" does not exist`, ""))
						}
					})
				}
				// A valid child still reaches the actual missing/denied schema owner.
				q := "SELECT " + schema + "." + name + "(ARRAY[1]" + tail + ")"
				code, message := "3F000", `schema "ai_review_absent" does not exist`
				if schema == "ai_review_private" {
					code, message = "42501", "permission denied for schema ai_review_private"
				}
				arrayInspectDiagnosticBoth(t, p, b, c, q, arrayInspectDiagnostic(q, schema, code, message, ""))
			}
		}
	})
}
