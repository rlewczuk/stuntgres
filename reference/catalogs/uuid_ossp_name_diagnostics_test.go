package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUUIDOsspNameDiagnostics(t *testing.T) {
	uuidNameCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidOsspNamespaceInstall(t, p, c, r)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO uuid_ossp_part,public", 0))
		// REL_18_6 parse_func.c/uuid.c expectations confirmed by the pinned
		// PostgreSQL18.6 UTF8/C oracle, including all unspecified empty fields.
		for _, f := range uuidNameFunctions {
			valid := "'" + uuidNameDNS + "','www.example.com'"
			for _, tc := range []struct{ label, tail, signature string }{
				{"zero", "()", "()"}, {"one_null", "(NULL)", "(unknown)"},
				{"typed_text", "(NULL::text,'x'::text)", "(text, text)"},
				{"integer_name", "(NULL::uuid,1)", "(uuid, integer)"},
				{"uuid_name", "(NULL::uuid,NULL::uuid)", "(uuid, uuid)"},
				{"three", "(NULL::uuid,'x',NULL)", "(uuid, unknown, unknown)"},
				{"unknown_label", "(wrong => NULL,name => 'x')", "(wrong => unknown, name => unknown)"},
				{"miscased_label", "(\"Namespace\" => NULL,name => 'x')", "(Namespace => unknown, name => unknown)"},
				{"positional_named_duplicate", "(NULL,namespace => NULL)", "(unknown, namespace => unknown)"},
				{"variadic", "(VARIADIC NULL::uuid[])", "(uuid[])"},
				{"star", "(*)", "()"},
			} {
				for _, prefix := range []string{"", "/*é*/ "} {
					t.Run(f.name+"/"+tc.label+"/"+prefix, func(t *testing.T) {
						pos := 8
						if prefix != "" {
							pos += 6
						}
						uuidOsspFailure(t, p, c, prefix+"SELECT "+f.name+tc.tail, uuidOsspMissing(f.name+tc.signature, pos))
					})
				}
			}
			for _, tc := range []struct {
				label, tail, code, message string
				position                   int
			}{
				{"bad_literal", "('bad','x')", "22P02", "invalid input syntax for type uuid: \"bad\"", 25},
				{"filter", "(" + valid + ") FILTER (WHERE TRUE)", "42809", "FILTER specified, but " + f.name + " is not an aggregate function", 8},
				{"over", "(" + valid + ") OVER ()", "42809", "OVER specified, but " + f.name + " is not a window function nor an aggregate function", 8},
				{"distinct", "(DISTINCT " + valid + ")", "42809", "DISTINCT specified, but " + f.name + " is not an aggregate function", 8},
				{"too_many", "(" + strings.Repeat("NULL,", 100) + "NULL)", "54023", "cannot pass more than 100 arguments to a function", 8},
				{"duplicate_label", "(name => 'x',name => 'y')", "42601", "argument name \"name\" used more than once", 37},
				{"positional_after_named", "(namespace => NULL,'x')", "42601", "positional argument cannot follow named argument", 43},
				{"reordered_bad_literal", "(name => 'x',namespace => 'bad')", "22P02", "invalid input syntax for type uuid: \"bad\"", 50},
				{"bad_literal_null_name", "('bad',NULL)", "22P02", "invalid input syntax for type uuid: \"bad\"", 25},
				{"reordered_bad_literal_null_name", "(name => NULL,namespace => 'bad')", "22P02", "invalid input syntax for type uuid: \"bad\"", 51},
			} {
				for _, prefix := range []string{"", "/*é*/ "} {
					t.Run(f.name+"/"+tc.label+"/"+prefix, func(t *testing.T) {
						d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						pos := tc.position
						if prefix != "" {
							pos += 6
						}
						d.Position = fixture.Position(pos)
						uuidOsspFailure(t, p, c, prefix+"SELECT "+f.name+tc.tail, d)
					})
				}
			}
			t.Run(f.name+"/bound_namespace", func(t *testing.T) {
				q := "SELECT " + f.name + "($1::uuid,$2::text) AS u"
				h := uuidNameHeld(t, p, c, q)
				defer h.Statement.Close()
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type uuid: \"bad\"")
				d.Context = "unnamed portal parameter $1 = '...'"
				rows, err := h.Statement.QueryContext(p.Ctx, "bad", "www.example.com")
				if rows != nil {
					rows.Close()
					t.Error("bad binding published rows")
				}
				fixture.AssertDiagnostic(t, err, &d)
				fixture.RunStep(t, p.Ctx, h, r, uuidNameStep(q, f.example, uuidNameDNS, "www.example.com"))
			})
			fixture.RunStep(t, p.Ctx, c, r, uuidNameStep("SELECT "+f.name+"('"+uuidNameDNS+"','www.example.com') AS u", f.example))
		}
	})
}
