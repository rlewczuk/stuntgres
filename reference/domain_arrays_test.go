package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceDomainArrays(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE DOMAIN domain_s.da AS name[]", 0))
		run(referenceExec("CREATE DOMAIN domain_s.sized AS pg_catalog.name[2][3]", 0))
		run(referenceExec("CREATE TABLE domain_s.arr (id integer,a domain_s.da)", 0))
		cases := []struct{ input, output string }{
			{"{}", "{}"},
			{`{NULL,"NULL","",alpha}`, `{NULL,"NULL","",alpha}`},
			{`{{a,b},{c,NULL}}`, `{{a,b},{c,NULL}}`},
			{`[0:1]={a,b}`, `[0:1]={a,b}`},
			{`[-1:0][3:4]={{a,b},{c,d}}`, `[-1:0][3:4]={{a,b},{c,d}}`},
			{`{ alpha , "a,b", "a{b}", "a\\b", "a\"b", " a " }`, `{alpha,"a,b","a{b}","a\\b","a\"b"," a "}`},
			{"{" + strings.Repeat("é", 32) + "}", "{" + strings.Repeat("é", 31) + "}"},
			{"{" + strings.Repeat("a", 64) + "}", "{" + strings.Repeat("a", 63) + "}"},
			{"{{}}", "{}"},
			{"{{},{}}", "{}"},
		}
		for i, tc := range cases {
			for _, prepared := range []bool{false, true} {
				s := referenceExec("INSERT INTO domain_s.arr VALUES ($1,$2)", 1, int64(i*2+1), tc.input)
				if prepared {
					s.args[0] = int64(i*2 + 2)
					s = referencePrepared(s)
				}
				run(s)
				q := referenceSelect("SELECT $1::domain_s.da AS a,$1::domain_s.sized AS sized FROM public.one", []string{"a", "sized"}, []string{"_NAME", "_NAME"}, [][]any{{tc.output, tc.output}}, tc.input)
				if prepared {
					q = referencePrepared(q)
				}
				run(q)
			}
		}
		run(referenceExec("INSERT INTO domain_s.arr VALUES (100,NULL),(101,$1)", 2, nil))
		want := make([][]any, 0, len(cases)*2+2)
		for i, tc := range cases {
			want = append(want, []any{int64(i*2 + 1), tc.output}, []any{int64(i*2 + 2), tc.output})
		}
		want = append(want, []any{int64(100), nil}, []any{int64(101), nil})
		s := referenceSelect("SELECT id,a FROM domain_s.arr ORDER BY id", []string{"id", "a"}, []string{"INT4", "_NAME"}, want)
		run(s)
		run(referencePrepared(s))
		run(referenceSelect("SELECT '{NULL,\"NULL\",\"\",alpha}'::domain_s.da AS a FROM public.one", []string{"a"}, []string{"_NAME"}, [][]any{{`{NULL,"NULL","",alpha}`}}))
		for _, prepared := range []bool{false, true} {
			want := controlsReferenceDiagnostic("2202E", "upper bound cannot be less than lower bound")
			want.Position = "8"
			s := referenceErrorDiagnostic("SELECT '[1:0]={}'::domain_s.da AS a FROM public.one", want)
			s.selectRows = true
			s.prepared = prepared
			s.errorStage = "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			run(s)
		}
		run(referenceExec("SET search_path TO domain_s,public", 0))
		run(referenceExec("INSERT INTO domain_s.arr VALUES (102,current_schemas(false))", 1))
		run(referenceSelect("SELECT a FROM domain_s.arr WHERE id=102", []string{"a"}, []string{"_NAME"}, [][]any{{"{domain_s,public}"}}))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ input, detail string }{
				{`{{a},{b,c}}`, "Multidimensional arrays must have sub-arrays with matching dimensions."},
				{`{a,,b}`, `Unexpected "," character.`},
			} {
				want := controlsReferenceDiagnostic("22P02", `malformed array literal: "`+tc.input+`"`)
				want.Detail = tc.detail
				want.Position = "8"
				s := referenceErrorDiagnostic("SELECT '"+tc.input+"'::domain_s.da AS a FROM public.one", want)
				s.selectRows = true
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				run(s)
				want.Position = ""
				want.Context = "unnamed portal parameter $1 = '...'"
				s = referenceErrorDiagnostic("INSERT INTO domain_s.arr VALUES (200,$1)", want, tc.input)
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
			}
			for _, tc := range []struct{ input, code, message, detail string }{
				{`[0:2]={a,b}`, "22P02", `malformed array literal: "[0:2]={a,b}"`, "Specified array dimensions do not match array contents."},
				{`[2:1]={}`, "2202E", "upper bound cannot be less than lower bound", ""},
				{`{"unterminated}`, "22P02", `malformed array literal: "{"unterminated}"`, "Unexpected end of input."},
				{`{a\}`, "22P02", `malformed array literal: "{a\}"`, "Unexpected end of input."},
			} {
				want := controlsReferenceDiagnostic(tc.code, tc.message)
				want.Detail = tc.detail
				want.Context = "unnamed portal parameter $1 = '...'"
				s := referenceErrorDiagnostic("SELECT $1::domain_s.da AS a FROM public.one", want, tc.input)
				s.selectRows = true
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
			}
		}
		run(referenceSelect("SELECT id FROM domain_s.arr WHERE id=200", []string{"id"}, []string{"INT4"}, [][]any{}))
		if backend.pg {
			// Explicit comparator/constructor/subscript controls: not general arrays.
			run(referenceSelect("SELECT ARRAY['a'::name,'b'::name]::domain_s.da AS made,('{a,b}'::domain_s.da)[1] AS first", []string{"made", "first"}, []string{"_NAME", "NAME"}, [][]any{{"{a,b}", "a"}}))
			run(referenceSelect("SELECT a FROM domain_s.arr WHERE id IN (1,3) ORDER BY a", []string{"a"}, []string{"_NAME"}, [][]any{{"{}"}, {`{NULL,"NULL","",alpha}`}}))
		} else {
			run(referenceError("SELECT a FROM domain_s.arr ORDER BY a", "42883"))
			run(referenceError("SELECT a FROM domain_s.arr WHERE id=999 ORDER BY a", "42883"))
		}
	})
}
