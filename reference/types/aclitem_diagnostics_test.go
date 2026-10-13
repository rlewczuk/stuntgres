package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceACLItemDiagnostics(t *testing.T) {
	aclitemScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.one (v INT)", 0))
		run(fixture.Exec("INSERT INTO public.one VALUES (1)", 1))
		run(fixture.Exec("CREATE TABLE public.acl_empty (v INT)", 0))
		run(fixture.Exec("CREATE TABLE public.acl_atomic (id INT,a aclitem,aa aclitem[])", 0))
		run(fixture.Exec("INSERT INTO public.acl_atomic VALUES (7,'=r/postgres','{NULL}')", 1))
		cases := []struct{ name, input, typ, code, message, detail, hint string }{
			{"keyword", "bogus", "aclitem", "22P02", `unrecognized key word: "bogus"`, "", `ACL key word must be "group" or "user".`},
			{"empty", "", "aclitem", "22P02", `unrecognized key word: ""`, "", `ACL key word must be "group" or "user".`},
			{"missing_name", "user ", "aclitem", "22P02", "missing name", "", `A name must follow the "group" or "user" key word.`},
			{"missing_equal", "user postgres r", "aclitem", "22P02", `missing "=" sign`, "", ""},
			{"mode", "postgres=z/postgres", "aclitem", "22P02", `invalid mode character: must be one of "arwdDxtXUCTcsAm"`, "", ""},
			{"digit_mode", "postgres=r1/postgres", "aclitem", "22P02", "extra garbage at the end of the ACL specification", "", ""},
			{"equal_mode", "postgres=r=/postgres", "aclitem", "22P02", "extra garbage at the end of the ACL specification", "", ""},
			{"mode_whitespace", "postgres= r/postgres", "aclitem", "22P02", "extra garbage at the end of the ACL specification", "", ""},
			{"slash_whitespace", "postgres=r /postgres", "aclitem", "22P02", "extra garbage at the end of the ACL specification", "", ""},
			{"unknown_grantee", "missing_acl_role=r/postgres", "aclitem", "42704", `role "missing_acl_role" does not exist`, "", ""},
			{"unknown_grantor", "postgres=r/missing_acl_role", "aclitem", "42704", `role "missing_acl_role" does not exist`, "", ""},
			{"decoded_role", `"missing ""acl\é"=r/postgres`, "aclitem", "42704", `role "missing "acl\é" does not exist`, "", ""},
			{"empty_grantor", "postgres=r/", "aclitem", "22P02", `a name must follow the "/" sign`, "", ""},
			{"quoted_empty_grantor", `postgres=r/""`, "aclitem", "22P02", `a name must follow the "/" sign`, "", ""},
			{"terminal", "postgres=r/postgres!", "aclitem", "22P02", "extra garbage at the end of the ACL specification", "", ""},
			{"long_ascii", strings.Repeat("n", 64) + "=r/postgres", "aclitem", "42622", "identifier too long", "Identifier must be less than 64 characters.", ""},
			{"long_utf8", strings.Repeat("é", 32) + "=r/postgres", "aclitem", "42622", "identifier too long", "Identifier must be less than 64 characters.", ""},
			{"mode_before_lookup", "missing_acl_role=z/missing_acl_grantor", "aclitem", "22P02", `invalid mode character: must be one of "arwdDxtXUCTcsAm"`, "", ""},
			{"grantee_before_grantor", "missing_acl_role=r/", "aclitem", "42704", `role "missing_acl_role" does not exist`, "", ""},
			{"array_child", "{postgres=r/postgres,missing_acl_role=r/postgres}", "aclitem[]", "42704", `role "missing_acl_role" does not exist`, "", ""},
			{"array_comma", "{postgres=r/postgres,,NULL}", "aclitem[]", "22P02", `malformed array literal: "{postgres=r/postgres,,NULL}"`, `Unexpected "," character.`, ""},
			{"array_ragged", "{{postgres=r/postgres},{NULL,NULL}}", "aclitem[]", "22P02", `malformed array literal: "{{postgres=r/postgres},{NULL,NULL}}"`, "Multidimensional arrays must have sub-arrays with matching dimensions.", ""},
			{"array_child_before_shape", "{{postgres=r/postgres},{missing_acl_role=r/postgres,NULL}}", "aclitem[]", "42704", `role "missing_acl_role" does not exist`, "", ""},
			{"array_shape_before_child", "{{postgres=r/postgres},{{missing_acl_role=r/postgres}}}", "aclitem[]", "22P02", `malformed array literal: "{{postgres=r/postgres},{{missing_acl_role=r/postgres}}}"`, "Multidimensional arrays must have sub-arrays with matching dimensions.", ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				want := aclitemDiagnostic(tc.code, tc.message)
				want.Detail = tc.detail
				want.Hint = tc.hint
				for _, source := range []string{"public.one", "public.acl_empty"} {
					for _, prefix := range []string{"", "/*é*/ "} {
						for _, prepared := range []bool{false, true} {
							for _, queryMode := range []bool{false, true} {
								name := source + "_" + fixture.Position(len(prefix)) + "_" + fixture.Position(1+boolInt(prepared)*2+boolInt(queryMode))
								t.Run(name, func(t *testing.T) {
									d := want
									d.Position = fixture.Position(8 + utf8.RuneCountInString(prefix))
									s := fixture.ErrorDiagnostic(prefix+"SELECT "+fixture.ArrayReferenceQuote(tc.input)+"::"+tc.typ+" AS a FROM "+source, d)
									s.SelectRows = queryMode
									s.Prepared = prepared
									s.ErrorStage = "execute"
									if prepared {
										s.ErrorStage = "prepare"
									}
									fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
								})
							}
						}
					}
				}
				for _, prepared := range []bool{false, true} {
					for _, queryMode := range []bool{false, true} {
						t.Run("bind_"+fixture.Position(1+boolInt(prepared)*2+boolInt(queryMode)), func(t *testing.T) {
							d := want
							d.Context = "unnamed portal parameter $1 = '...'"
							if tc.input == "" {
								d.Context = "unnamed portal parameter $1 = ''"
							}
							s := fixture.ErrorDiagnostic("SELECT $1::"+tc.typ+" AS a FROM public.one", d, tc.input)
							s.Prepared = prepared
							s.SelectRows = queryMode
							s.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						})
					}
				}
			})
		}
		for _, tc := range []struct{ input, typ, code, message string }{
			{"missing_acl_role=r/postgres", "aclitem", "42704", `role "missing_acl_role" does not exist`},
			{"postgres=z/postgres", "aclitem", "22P02", `invalid mode character: must be one of "arwdDxtXUCTcsAm"`},
			{"{missing_acl_role=r/postgres}", "aclitem[]", "42704", `role "missing_acl_role" does not exist`},
		} {
			for _, prepared := range []bool{false, true} {
				for _, bind := range []bool{false, true} {
					t.Run("atomic_"+tc.typ+"_"+tc.code+"_"+fixture.Position(1+boolInt(prepared)*2+boolInt(bind)), func(t *testing.T) {
						column, good := "a", "'=r/postgres'"
						if tc.typ == "aclitem[]" {
							column = "aa"
							good = "'{NULL}'"
						}
						prefix := "INSERT INTO public.acl_atomic (id," + column + ") VALUES (1," + good + "),(2,"
						d := aclitemDiagnostic(tc.code, tc.message)
						q := prefix + fixture.ArrayReferenceQuote(tc.input) + "::" + tc.typ + ")"
						var args []any
						if bind {
							q = prefix + "$1::" + tc.typ + ")"
							args = []any{tc.input}
							d.Context = "unnamed portal parameter $1 = '...'"
						} else {
							d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
						}
						s := fixture.ErrorDiagnostic(q, d, args...)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared && !bind {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,a,aa FROM public.acl_atomic ORDER BY id", []string{"id", "a", "aa"}, []string{"INT4", "ACLITEM", "_ACLITEM"}, [][]any{{int64(7), "=r/postgres", "{NULL}"}}))
					})
				}
			}
		}
	})
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
