package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func regLookupRelTypeErrorRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, input, kind, valid string, output any, want fixture.Diagnostic) {
	t.Helper()
	for _, route := range []string{"literal", "inferred-text", "explicit-text"} {
		t.Run(route, func(t *testing.T) {
			q := "SELECT " + name + "($1) AS value"
			args := []any{input}
			if route == "explicit-text" {
				q = "SELECT " + name + "($1::text) AS value"
			}
			if route == "literal" {
				q, args = "SELECT /* é🙂 */ "+name+"("+regObjectLiteral(input)+") AS value", nil
			}
			s := fixture.ErrorDiagnostic(q, want, args...)
			s.SelectRows, s.ErrorStage = true, "execute"
			regObjectBoth(t, p, b, c, s)
			// Successful Prepare is part of the contract even for malformed
			// destination text. The error belongs to each reached execution.
			held := regLookupHeld(t, p, c, q)
			defer held.Statement.Close()
			regObjectRunStep(t, p, b, held, s)
			if route != "literal" {
				regObjectRunStep(t, p, b, held, regLookupRelTypeSelect(b, q, []string{"value"}, []string{kind}, [][]any{{nil}}, nil))
				regObjectRunStep(t, p, b, held, regLookupRelTypeSelect(b, q, []string{"value"}, []string{kind}, [][]any{{output}}, valid))
			}
		})
	}
}

func TestReferenceRegLookupRelTypeDiagnostics(t *testing.T) {
	regLookupScenario(t, nil, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ input, code, message string }{
			{"a.b.c.d", "42601", "improper relation name (too many dotted names): a.b.c.d"},
			{"otherdb.public.t", "0A000", `cross-database references are not implemented: "otherdb.public.t"`},
		} {
			t.Run("to_regclass/"+tc.input, func(t *testing.T) {
				regLookupRelTypeErrorRoutes(t, p, b, c, "to_regclass", tc.input, "REGCLASS", "pg_catalog.pg_class", "pg_class", fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
			})
		}
		for _, tc := range []struct {
			input, code, message, position string
			parseContext                   bool
		}{
			{"x.y.z.w", "42601", "improper qualified name (too many dotted names): x.y.z.w", "", false},
			{"otherdb.public.t", "0A000", "cross-database references are not implemented: otherdb.public.t", "", false},
			{"integer(4)", "42601", `syntax error at or near "("`, "8", true},
			{"int4(", "42601", "syntax error at end of input", "6", true},
			{"integer..x", "42601", `syntax error at or near ".."`, "8", true},
			{"varchar('12')", "42601", `syntax error at or near "'12'"`, "9", true},
			{"varchar((12))", "42601", `syntax error at or near "("`, "9", true},
			{"int4(4)", "42601", `type modifier is not allowed for type "int4"`, "", false},
			{"numeric(1+2)", "42601", "type modifiers must be simple constants or identifiers", "", false},
			{"varchar(0)", "22023", "length for type varchar must be at least 1", "", false},
			{"numeric(0)", "22023", "NUMERIC precision 0 must be between 1 and 1000", "", false},
			{"bit(0)", "22023", "length for type bit must be at least 1", "", false},
			{"pg_catalog.timestamp(1,2)", "22023", "invalid type modifier", "", false},
			{"float(54)", "22023", "precision for type float must be less than 54 bits", "7", true},
			{"interval(-1)", "42601", `syntax error at or near "-"`, "10", true},
			{" 23 ", "42601", `syntax error at or near "23"`, "2", true},
			{"+23", "42601", `syntax error at or near "+"`, "1", true},
			{"-1", "42601", `syntax error at or near "-"`, "1", true},
			{"0x17", "42601", `syntax error at or near "0x17"`, "1", true},
			{"/* é🙂 */ integer(4)", "42601", `syntax error at or near "("`, "17", true},
		} {
			for _, name := range []string{"to_regtype", "to_regtypemod"} {
				t.Run(name+"/"+tc.input, func(t *testing.T) {
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Position = tc.position
					if tc.parseContext {
						want.Context = `invalid type name "` + tc.input + `"`
					}
					kind, valid := "REGTYPE", "integer"
					var output any = "integer"
					if name == "to_regtypemod" {
						kind, valid, output = "INT4", "varchar(12)", int64(16)
					}
					regLookupRelTypeErrorRoutes(t, p, b, c, name, tc.input, kind, valid, output, want)
				})
			}
		}
		for _, input := range []string{"23", "-"} {
			t.Run("typmod-no-oid-shortcut/"+input, func(t *testing.T) {
				want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+input+`"`)
				want.Position, want.Context = "1", `invalid type name "`+input+`"`
				regLookupRelTypeErrorRoutes(t, p, b, c, "to_regtypemod", input, "INT4", "varchar(12)", int64(16), want)
			})
		}
		for _, tc := range []struct {
			name, cast, code, message, valid, kind string
			output                                 any
		}{
			{"to_regclass", "regclass", "42P01", `relation "rl_missing_child" does not exist`, "pg_catalog.pg_class", "REGCLASS", "pg_class"},
			{"to_regnamespace", "regnamespace", "3F000", `schema "rl_missing_child" does not exist`, "pg_catalog", "REGNAMESPACE", "pg_catalog"},
			{"to_regtype", "regtype", "42704", `type "rl_missing_child" does not exist`, "integer", "REGTYPE", "integer"},
			{"to_regtypemod", "regtype", "42704", `type "rl_missing_child" does not exist`, "integer", "INT4", int64(-1)},
		} {
			t.Run(tc.name+"/child-error", func(t *testing.T) {
				q := "SELECT " + tc.name + "(($1::text::" + tc.cast + ")::text) AS value"
				s := regLookupError(q, tc.code, tc.message, "rl_missing_child")
				regObjectBoth(t, p, b, c, s)
				held := regLookupHeld(t, p, c, q)
				defer held.Statement.Close()
				regObjectRunStep(t, p, b, held, s)
				regObjectRunStep(t, p, b, held, regLookupRelTypeSelect(b, q, []string{"value"}, []string{tc.kind}, [][]any{{tc.output}}, tc.valid))
			})
		}
	})
}
