package reference_test

import (
	"database/sql"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceACLItemBoundaries(t *testing.T) {
	aclitemScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.acl_boundary (a aclitem,aa aclitem[])", 0))
		run(fixture.Exec("INSERT INTO public.acl_boundary VALUES ('=r/postgres','{=r/postgres}')", 1))
		run(fixture.Select("SELECT a,aa FROM public.acl_boundary", []string{"a", "aa"}, []string{"ACLITEM", "_ACLITEM"}, [][]any{{"=r/postgres", "{=r/postgres}"}}))
		for _, op := range []string{"<>", "!=", "<", "<=", ">", ">="} {
			for _, prepared := range []bool{false, true} {
				for _, parameter := range []bool{false, true} {
					t.Run(op+"_"+fixture.Position(1+boolInt(prepared)*2+boolInt(parameter)), func(t *testing.T) {
						left, right := "'=r/postgres'::aclitem", "'=r/postgres'::aclitem"
						var args []any
						if parameter {
							left = "$1::aclitem"
							right = "$2::aclitem"
							args = []any{"=r/postgres", "=r/postgres"}
						}
						normalized := op
						if op == "!=" {
							normalized = "<>"
						}
						d := aclitemDiagnostic("42883", "operator does not exist: aclitem "+normalized+" aclitem")
						d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
						d.Position = fixture.Position(utf8.RuneCountInString("SELECT "+left+" ") + 1)
						s := fixture.ErrorDiagnostic("SELECT "+left+" "+op+" "+right, d, args...)
						s.Prepared = prepared
						s.SelectRows = true
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					})
				}
				// Unsupported contextual operator is resolved before unknown text input.
				t.Run(op+"_context_"+fixture.Position(1+boolInt(prepared)), func(t *testing.T) {
					prefix := "SELECT '=r/postgres'::aclitem "
					normalized := op
					if op == "!=" {
						normalized = "<>"
					}
					d := aclitemDiagnostic("42883", "operator does not exist: aclitem "+normalized+" unknown")
					d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
					s := fixture.ErrorDiagnostic(prefix+op+" 'missing_acl_role=r/postgres'", d)
					s.Prepared = prepared
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				})
			}
		}
		for _, op := range []string{"<", "<=", ">", ">="} {
			for _, prepared := range []bool{false, true} {
				t.Run("array_"+op+"_"+fixture.Position(1+boolInt(prepared)), func(t *testing.T) {
					d := aclitemDiagnostic("42883", "could not identify a comparison function for type aclitem")
					s := fixture.ErrorDiagnostic("SELECT $1::aclitem[] "+op+" $2::aclitem[]", d, "{=r/postgres}", "{=r/postgres}")
					s.Prepared = prepared
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				})
			}
		}
	})
}
