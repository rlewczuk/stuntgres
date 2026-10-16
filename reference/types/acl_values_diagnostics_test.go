package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceACLValuesDiagnostics(t *testing.T) {
	aclValuesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, input := range []string{"", " ", ",SELECT", "SELECT,", "SELECT,,UPDATE", "ALL", "SELECT WITH GRANT OPTION", "ALTER  SYSTEM", "ALTER\tSYSTEM", "\u00a0SELECT"} {
			t.Run("privilege_"+input, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				chunk := strings.Trim(input, " \t\n\r\v\f")
				if strings.Contains(input, ",") {
					chunk = ""
				}
				d := aclitemDiagnostic("22023", `unrecognized privilege type: "`+chunk+`"`)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("SELECT makeaclitem(10,10,$1::text,false)", d, input)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
			})
		}
		for _, code := range []string{"z", "R", ""} {
			t.Run("code_"+code, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				d := aclitemDiagnostic("XX000", "unrecognized object type abbreviation: "+code)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("SELECT acldefault($1::\"char\",10)", d, code)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
			})
		}
		owner := aclValuesOwner(b.PG)
		run(fixture.Select("SELECT acldefault('relation'::\"char\",10) AS a", []string{"a"}, []string{"_ACLITEM"}, [][]any{{"{" + owner + "=arwdDxtm/" + owner + "}"}}))
		for _, expr := range []string{"'{}'::aclitem[]", "'{{NULL}}'::aclitem[]", "'{{NULL,NULL},{NULL,NULL}}'::aclitem[]", "'{NULL}'::aclitem[]"} {
			for _, consumer := range []string{"SELECT * FROM aclexplode(" + expr + ")", "SELECT " + expr + " @> makeaclitem(10,10,'SELECT',false)"} {
				t.Run(consumer, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					d := aclitemDiagnostic("22023", "ACL arrays must be one-dimensional")
					if expr == "'{NULL}'::aclitem[]" {
						d = aclitemDiagnostic("22004", "ACL arrays must not contain null values")
					}
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(consumer, d)
						s.Prepared = prepared
						s.SelectRows = true
						s.ErrorStage = "execute"
						run(s)
					}
				})
			}
		}
		for _, tc := range []struct{ call, signature string }{
			{"makeaclitem()", "makeaclitem()"},
			{"makeaclitem(10,10,'SELECT')", "makeaclitem(integer, integer, unknown)"},
			{"acldefault(true,10)", "acldefault(boolean, integer)"},
			{"acldefault('r'::text,10)", "acldefault(text, integer)"},
			{"aclexplode(true)", "aclexplode(boolean)"},
			{"\"MAKEACLITEM\"(10,10,'SELECT',false)", "MAKEACLITEM(integer, integer, unknown, boolean)"},
			{"public.makeaclitem(10,10,'SELECT',false)", "public.makeaclitem(integer, integer, unknown, boolean)"},
			{"makeaclitem(grantee=>10,grantor=>10,privileges=>'SELECT',is_grantable=>false)", "makeaclitem(grantee => integer, grantor => integer, privileges => unknown, is_grantable => boolean)"},
			{"acldefault(type=>'r',ownerid=>10)", "acldefault(type => unknown, ownerid => integer)"},
			{"aclexplode(wrong=>NULL::aclitem[])", "aclexplode(wrong => aclitem[])"},
		} {
			t.Run(tc.call, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, prefix := range []string{"", "/*é*/ "} {
					for _, prepared := range []bool{false, true} {
						d := aclitemDiagnostic("42883", "function "+tc.signature+" does not exist")
						d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						d.Position = fixture.Position(8 + utf8.RuneCountInString(prefix))
						s := fixture.ErrorDiagnostic(prefix+"SELECT "+tc.call, d)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
					}
				}
			})
		}
		run(aclValuesExplode("SELECT * FROM pg_catalog.aclexplode(acl=>pg_catalog.acldefault('c',0))", nil))
		for _, prefix := range []string{"", "/*é*/ "} {
			left := "SELECT makeaclitem(10,10,'SELECT',false) "
			d := aclitemDiagnostic("42883", "operator does not exist: aclitem @> aclitem")
			d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			d.Position = fixture.Position(utf8.RuneCountInString(prefix+left) + 1)
			s := fixture.ErrorDiagnostic(prefix+left+"@> makeaclitem(10,10,'SELECT',false)", d)
			run(s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
		}
		t.Run("unknown_overload", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			d := aclitemDiagnostic("42725", "operator is not unique: unknown @> unknown")
			d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
			d.Position = "13"
			run(fixture.ErrorDiagnostic("SELECT '{}' @> '{}'", d))
		})
		t.Run("known_array_unknown_right", func(t *testing.T) {
			q := "SELECT ARRAY[makeaclitem(10,10,'SELECT',false)] @> " + fixture.ArrayReferenceQuote(owner+"=r/"+owner) + " AS a"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, []string{"a"}, []string{"BOOL"}, [][]any{{true}}))
		})
		t.Run("unknown_left_known_scalar", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			q := "SELECT " + fixture.ArrayReferenceQuote("{"+owner+"=r/"+owner+"}") + " @> makeaclitem(10,10,'SELECT',false) AS a"
			run(fixture.Select(q, []string{"a"}, []string{"BOOL"}, [][]any{{true}}))
		})
		for _, tc := range []struct{ name, query, code, message, token string }{
			{"positional_after_named", "SELECT aclexplode(acl=>NULL::aclitem[],NULL)", "42601", "positional argument cannot follow named argument", "NULL)"},
			{"duplicate_label", "SELECT aclexplode(acl=>NULL::aclitem[],acl=>NULL::aclitem[])", "42601", `argument name "acl" used more than once`, "acl=>NULL::aclitem[])"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, prepared := range []bool{false, true} {
					d := aclitemDiagnostic(tc.code, tc.message)
					d.Position = fixture.Position(utf8.RuneCountInString(tc.query[:strings.LastIndex(tc.query, tc.token)]) + 1)
					s := fixture.ErrorDiagnostic(tc.query, d)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			})
		}
		t.Run("non_ascii_internal_char", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			// Source-derived first-byte expectation; pinned discovery must resolve
			// protocol rendering of the invalid standalone UTF8 byte.
			d := aclitemDiagnostic("XX000", "unrecognized object type abbreviation: \xc3")
			run(fixture.ErrorDiagnostic("SELECT acldefault($1::\"char\",10)", d, "é"))
		})
		run(fixture.Exec("CREATE TABLE acl_values_atomic(id INT,a aclitem)", 0))
		run(fixture.Exec("INSERT INTO acl_values_atomic VALUES (7,NULL)", 1))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO acl_values_atomic VALUES (1,makeaclitem(10,10,'SELECT',false)),(2,makeaclitem(10,10,$1::text,false))", aclitemDiagnostic("22023", `unrecognized privilege type: "bad"`), "bad")
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Select("SELECT id,a FROM acl_values_atomic", []string{"id", "a"}, []string{"INT4", "ACLITEM"}, [][]any{{int64(7), nil}}))
		}
	})
}
