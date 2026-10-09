package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceFormatVariadic(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ call, want string }{
			{`format('%s/%L/%s',VARIADIC '{a,NULL,"NULL"}'::name[])`, "a/NULL/NULL"},
			{`format('%4$s/%1$s',VARIADIC '{{a,b},{c,d}}'::name[])`, "d/a"},
			{`format('%2$s/%s',VARIADIC '[0:2]={a,b,c}'::name[])`, "b/c"},
			{`format('%s/%s/%s/%s',VARIADIC '{"a,b","a\"b","a\\b",""}'::name[])`, "a,b/a\"b/a\\b/"},
			{`format('ok',VARIADIC NULL::name[])`, "ok"},
			{`format('ok',VARIADIC '{}'::name[])`, "ok"},
			{`format('|%*s|',VARIADIC '{5,x}'::name[])`, "|    x|"},
			{`format('%s',VARIADIC current_schemas(false))`, "public"},
		} {
			t.Run(tc.call, func(t *testing.T) { formatReferenceBoth(t, p, b, c, formatReferenceText(tc.call, tc.want)) })
		}
		for _, value := range []string{"NULL::name[]", "'{}'::name[]"} {
			t.Run(value, func(t *testing.T) {
				formatReferenceFailure(t, p, b, c, "SELECT format('%s',VARIADIC "+value+")", controlsReferenceDiagnostic("22023", "too few arguments for format()"), false)
			})
		}
		t.Run("200-elements", func(t *testing.T) {
			array := "{" + strings.Repeat("x,", 199) + "last}"
			formatReferenceBoth(t, p, b, c, formatReferenceText("format('%200$s/%1$s',VARIADIC '"+array+"'::name[])", "last/x"))
		})
		t.Run("preceding-ordinary-argument", func(t *testing.T) {
			d := controlsReferenceDiagnostic("42883", "function format(unknown, unknown, name[]) does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "8"
			formatReferenceFailure(t, p, b, c, "SELECT format('%s/%s','x',VARIADIC '{a,b}'::name[])", d, true)
		})
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE DOMAIN format_names AS name[]", 0))
		for _, tc := range []struct {
			call string
			want any
		}{
			{`format('%s/%L/%s',VARIADIC '{a,NULL,"NULL"}'::format_names)`, "a/NULL/NULL"},
			{`format('%s','{a,b}'::format_names)`, "{a,b}"},
			{`format('ok',VARIADIC NULL::format_names)`, "ok"},
			{`format(NULL,VARIADIC '{a,b}'::format_names)`, nil},
		} {
			t.Run(tc.call, func(t *testing.T) { formatReferenceBoth(t, p, b, c, formatReferenceText(tc.call, tc.want)) })
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE format_arrays(id int,a format_names)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO format_arrays VALUES(1,'{a,b}'),(2,NULL)", 2))
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT id,format('ok',VARIADIC a) AS v FROM format_arrays ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "ok"}, {int64(2), "ok"}}))
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT format('%2$s/%1$s',VARIADIC a) AS v FROM format_arrays WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{"b/a"}}))
	})
}

func TestReferenceFormatEvaluation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE SEQUENCE format_eval_seq", 0))
				q := "SELECT format($1::text,nextval('format_eval_seq'),nextval('format_eval_seq')) AS v"
				var executor referenceExecutor = c
				if prepared {
					stmt, err := c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
					// Prepare must not demand either nextval child.
					d := controlsReferenceDiagnostic("55000", `currval of sequence "format_eval_seq" is not yet defined in this session`)
					s := referenceErrorDiagnostic("SELECT currval('format_eval_seq')", d)
					s.selectRows, s.errorStage = true, "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				}
				for _, tc := range []struct {
					pattern any
					want    any
					last    int64
				}{
					{nil, nil, 2}, {"literal", "literal", 4}, {"%1$s/%1$s/%2$s", "5/5/6", 6}, {"%2$s/%1$s", "8/7", 8},
				} {
					referenceRunStep(t, p.ctx, executor, b.recorder, referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.pattern))
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT currval('format_eval_seq')", []string{"currval"}, []string{"INT8"}, [][]any{{tc.last}}))
				}
				for _, call := range []string{"format(NULL,format('%I',NULL))", "format('literal',format('%I',NULL))", "format('%1$s','x',format('%I',NULL))"} {
					t.Run(call, func(t *testing.T) {
						formatReferenceFailure(t, p, b, c, "SELECT "+call, controlsReferenceDiagnostic("22004", "null values cannot be formatted as an SQL identifier"), false)
					})
				}
				formatReferenceBoth(t, p, b, c, referenceSelect("SELECT CASE WHEN true THEN 'safe' ELSE format('%I',NULL) END AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"safe"}}))
				formatReferenceBoth(t, p, b, c, referenceSelect("SELECT COALESCE('safe',format('%I',NULL)) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"safe"}}))
			})
		})
	}
}
