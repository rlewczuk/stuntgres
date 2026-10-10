package reference_test

import (
	"testing"
)

// REL_18_0 src/test/regress/expected/jsonpath.out supplies these goldens;
// they are published expectations, not observations from this worktree.
var jsonpathReferenceOutputs = []struct{ input, output string }{
	{`$`, `$`}, {`lax $`, `$`}, {`(($))`, `$`}, {`strict $`, `strict $`},
	{`$.a.v`, `$."a"."v"`}, {`$[*][0].a.b`, `$[*][0]."a"."b"`},
	{`$.a.*`, `$."a".*`}, {`$.a.**{2 to 2}.b`, `$."a".**{2}."b"`},
	{`$.a.**{5 to last}.b`, `$."a".**{5 to last}."b"`},
	{`$.a[1,2, 3 to 16]`, `$."a"[1,2,3 to 16]`}, {`$[last]`, `$[last]`},
	{`$.g ? (@.a == 1)`, `$."g"?(@."a" == 1)`},
	{`$.g ? (exists (@.x))`, `$."g"?(exists (@."x"))`},
	{`$a.b`, `$"a"."b"`}, {`$+1`, `($ + 1)`}, {`$.a/+-1`, `($."a" / -1)`},
	{`1 * 2 + 4 % -3 != false`, `(1 * 2 + 4 % -3 != false)`},
	{`1.e1`, `10`}, {`0.0010e-1`, `0.00010`}, {`0b100101`, `37`},
	{`0o273`, `187`}, {`0x42F`, `1071`}, {`1_000_000`, `1000000`},
	{`0x1EEE_FFFF`, `518979583`},
	{`"\x50\u0067\u{53}\u{051}\u{00004C}"`, `"PgSQL"`},
	{`"\z"`, `"z"`}, {`"\ud83d\ude04\ud83d\udc36"`, `"😄🐶"`},
	{`$ ? (@ like_regex "pattern" flag "isim")`, `$?(@ like_regex "pattern" flag "ism")`},
	{`$ ? (@ like_regex "pattern" flag "")`, `$?(@ like_regex "pattern")`},
	{`$ ? (@ like_regex "pattern" flag "q")`, `$?(@ like_regex "pattern" flag "q")`},
	{`$ ? (@ like_regex "pattern" flag "iq")`, `$?(@ like_regex "pattern" flag "iq")`},
	{`$ ? (@ like_regex "pattern" flag "smixq")`, `$?(@ like_regex "pattern" flag "ismxq")`},
}

func TestReferenceJSONPathRepresentation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				run := func(s referenceStep) {
					if prepared {
						s = referencePrepared(s)
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, jsonpathReferenceStep(t, b, s))
				}
				run(referenceExec("CREATE TABLE jp_io(id INT,p pg_catalog.jsonpath)", 0))
				inquiry := func(s referenceStep) {
					s.prepared = prepared
					run(jsonpathReferenceInquiry(b, s))
				}
				inquiry(referenceSelect("SELECT typname::text,oid::bigint,typarray::bigint,typelem::bigint FROM pg_catalog.pg_type WHERE typname='jsonpath'", []string{"typname", "oid", "typarray", "typelem"}, []string{"TEXT", "INT8", "INT8", "INT8"}, [][]any{{"jsonpath", int64(4072), int64(4073), int64(0)}}))
				inquiry(referenceSelect("SELECT typname::text,oid::bigint,typarray::bigint,typelem::bigint FROM pg_catalog.pg_type WHERE typname='_jsonpath'", []string{"typname", "oid", "typarray", "typelem"}, []string{"TEXT", "INT8", "INT8", "INT8"}, [][]any{{"_jsonpath", int64(4073), int64(0), int64(4072)}}))
				inquiry(referenceSelect("SELECT atttypid::bigint,atttypmod,attcollation::bigint FROM pg_catalog.pg_attribute WHERE attrelid='jp_io'::regclass AND attname='p'", []string{"atttypid", "atttypmod", "attcollation"}, []string{"INT8", "INT4", "INT8"}, [][]any{{int64(4072), int64(-1), int64(0)}}))
				var want [][]any
				for i, tc := range jsonpathReferenceOutputs {
					run(referenceExec("INSERT INTO jp_io VALUES($1,$2)", 1, i, tc.input))
					want = append(want, []any{int64(i), tc.output})
					for _, expr := range []string{
						"jsonpath " + jsonReferenceQuote(tc.input),
						"pg_catalog.jsonpath " + jsonReferenceQuote(tc.input),
						"CAST(" + jsonReferenceQuote(tc.input) + " AS jsonpath)",
						jsonReferenceQuote(tc.input) + "::pg_catalog.jsonpath",
					} {
						run(referenceSelect("SELECT "+expr+" AS v", []string{"v"}, []string{"JSONPATH"}, [][]any{{tc.output}}))
					}
					run(referenceSelect("SELECT CAST($1 AS jsonpath) AS v", []string{"v"}, []string{"JSONPATH"}, [][]any{{tc.output}}, tc.input))
					run(referenceSelect("SELECT CAST(CAST($1 AS jsonpath) AS text)::jsonpath AS v", []string{"v"}, []string{"JSONPATH"}, [][]any{{tc.output}}, tc.output))
				}
				run(referenceExec("INSERT INTO jp_io(id) VALUES($1)", 1, len(want)))
				want = append(want, []any{int64(len(want)), nil})
				run(referenceSelect("SELECT id,p FROM jp_io ORDER BY id", []string{"id", "p"}, []string{"INT4", "JSONPATH"}, want))
			}
		})
	}
}
