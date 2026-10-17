package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUserOperatorEdgeDiagnostics(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq)", "CREATE OPERATOR public.#!# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4ne)", "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)")
		cases := []struct{ name, q, code, message string }{
			{"invalid_boolean", "CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,HASHES=foo)", "42601", "hashes requires a Boolean value"},
			{"alter_missing_link", "ALTER OPERATOR public.#=# (int4,int4) SET (COMMUTATOR=OPERATOR(public.#?#))", "42883", "operator does not exist: integer public.#?# integer"},
			{"alter_shell_link", "CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,COMMUTATOR=OPERATOR(public.#@#))", "", ""},
			{"reject_shell_reference", "ALTER OPERATOR public.#=# (int4,int4) SET (COMMUTATOR=OPERATOR(public.#@#))", "42883", "operator is only a shell: integer public.#@# integer"},
			{"set_negator", "ALTER OPERATOR public.#=# (int4,int4) SET (NEGATOR=OPERATOR(public.#!#))", "", ""},
			{"same_negator", "ALTER OPERATOR public.#=# (int4,int4) SET (NEGATOR=OPERATOR(public.#!#))", "", ""},
			{"changed_negator", "ALTER OPERATOR public.#=# (int4,int4) SET (NEGATOR=OPERATOR(pg_catalog.=))", "42P13", `operator attribute "negator" cannot be changed if it has already been set`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if tc.code == "" {
					userOperatorExec(t, p, b, c, tc.q)
				} else {
					userOperatorError(t, p, b, c, tc.q, tc.code, tc.message, "", "", "", false)
				}
				userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", int64(5))
			})
		}
		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		t.Run("unknown_create_attribute_warning", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("CREATE OPERATOR public.#/# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi,userop_unknown=true)", 0))
			want := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42601", Message: `operator attribute "userop_unknown" not recognized`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("complete warning got %#v want %#v", notices, want)
			}
			userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#/#) 3 AS v", "INT4", int64(-1))
		})
		t.Run("missing_drop_notice", func(t *testing.T) {
			notices = nil
			fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("DROP OPERATOR IF EXISTS public.#%# (int4,int4)", 0))
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "operator public.#%# does not exist, skipping"}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("complete notice got %#v want %#v", notices, want)
			}
		})
	})
}
