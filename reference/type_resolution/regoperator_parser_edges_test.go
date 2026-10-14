package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// These expectations follow REL_18_0 parseNameAndArgTypes/scanner_isspace,
// independently of native separation. The root grounds the full records on the
// pinned UTF8/C oracle before changing production input handling.
func TestReferenceRegOperatorParserEdges(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, tc := range []struct{ name, input, code, message string }{
				{"name_before_missing_right", "a..+(integer", "42602", "invalid name syntax"},
				{"name_before_empty_operand", "a..+(integer,)", "42602", "invalid name syntax"},
				{"name_before_missing_type", "a..+(no_such_regop_type,integer)", "42602", "invalid name syntax"},
				{"right_before_missing_type", "+(no_such_regop_type,integer", "22P02", "expected a right parenthesis"},
				{"first_type_before_empty_second", "+(no_such_regop_type,)", "42704", `type "no_such_regop_type" does not exist`},
				{"first_type_before_unbalanced_second", "+(no_such_regop_type,integer[)", "42704", `type "no_such_regop_type" does not exist`},
				{"first_type_before_unterminated_second", `+(no_such_regop_type,"unterminated)`, "42704", `type "no_such_regop_type" does not exist`},
				{"empty_first_before_missing_second", "+(,no_such_regop_type)", "42601", `invalid type name ""`},
				// The following strings contain actual U+00A0, not ASCII spaces
				// or a SQL escape. NONE is special only after ASCII trimming.
				{"nbsp_before_none", "-( NONE,integer)", "42704", "type \" none\" does not exist"},
				{"nbsp_after_none", "-(NONE ,integer)", "42704", "type \"none \" does not exist"},
				{"nbsp_before_type", "+( integer,integer)", "42704", "type \" integer\" does not exist"},
				{"nbsp_after_type", "+(integer ,integer)", "42704", "type \"integer \" does not exist"},
				{"nbsp_before_second_type", "-(NONE, integer)", "42704", "type \" integer\" does not exist"},
				{"nbsp_after_second_type", "-(NONE,integer )", "42704", "type \"integer \" does not exist"},
				{"nbsp_only_body", "+( )", "42704", "type \" \" does not exist"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					regoperatorParserEdgeRoutes(t, p, b, c, tc.input, func(q, position, portal string, args ...any) fixture.Step {
						return regoperatorError(q, tc.code, tc.message, "", position, portal, args...)
					})
				})
			}
			for _, tc := range []struct {
				name, input, text string
				oid               int64
			}{
				{"ascii_unary", " \t\n\r\v\f-( \t\n\r\v\fNoNe \t\n\r\v\f, \t\n\r\v\finteger \t\n\r\v\f) \t\n\r\v\f", "-(NONE,integer)", 558},
				{"ascii_binary", "+( \t\n\r\v\finteger \t\n\r\v\f, \t\n\r\v\finteger \t\n\r\v\f)", "+(integer,integer)", 551},
			} {
				t.Run(tc.name, func(t *testing.T) {
					regoperatorParserEdgeRoutes(t, p, b, c, tc.input, func(q, _, _ string, args ...any) fixture.Step {
						return regoperatorRows("SELECT v,v::oid AS o FROM ("+q+" AS v) AS regop_edge", "regoperator", tc.text, tc.oid, b.PG, args...)
					})
				})
			}
		})
	}
}

// Each route is its own subtest so an unknown full oracle record on one route
// does not hide the other records. RunStep prints the complete logical record on
// mismatch; no golden is learned and no diagnostic field is discarded.
func regoperatorParserEdgeRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, input string, step func(string, string, string, ...any) fixture.Step) {
	t.Helper()
	ctx := regoperatorContext(p, b)
	for _, route := range []struct {
		name, query, position, portal string
		prepared, held                bool
		args                          []any
	}{
		{"literal_query", "SELECT " + regoperatorLiteral(input) + "::regoperator", "8", "", false, false, nil},
		{"literal_prepare", "SELECT " + regoperatorLiteral(input) + "::regoperator", "8", "", true, false, nil},
		{"inferred_query", "SELECT $1::regoperator", "", "unnamed portal parameter $1 = '...'", false, false, []any{input}},
		{"inferred_held", "SELECT $1::regoperator", "", "unnamed portal parameter $1 = '...'", false, true, []any{input}},
		{"runtime_query", "SELECT $1::text::regoperator", "", "", false, false, []any{input}},
		{"runtime_prepare", "SELECT $1::text::regoperator", "", "", true, false, []any{input}},
	} {
		t.Run(route.name, func(t *testing.T) {
			s := step(route.query, route.position, route.portal, route.args...)
			if s.State != "" {
				s.ErrorStage = "execute"
				if route.name == "literal_prepare" {
					s.ErrorStage = "prepare"
				}
			}
			if route.prepared {
				s = fixture.Prepared(s)
			}
			if route.held {
				stmt, err := c.PrepareContext(ctx, s.Query)
				if err != nil {
					t.Fatal("parameter Prepare", err)
				}
				defer stmt.Close()
				fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
				return
			}
			fixture.RunStep(t, ctx, c, b.Recorder, s)
		})
	}
}
