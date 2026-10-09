package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceArraySyntax(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE DOMAIN public.array_di AS INT", 0))
		for i, declaration := range []string{"INTEGER[1]", "INT[][]", "pg_catalog.int4[2][3]", "INTEGER ARRAY", "INTEGER ARRAY[4]", "public.array_di[]", "NUMERIC(5,2)[]", "TIMESTAMP(3) WITH TIME ZONE[]", "INTERVAL DAY TO SECOND(3)[]"} {
			run(referenceExec(fmt.Sprintf("CREATE TABLE public.array_syntax_%d (a %s)", i, declaration), 0))
		}
		run(referenceExec("INSERT INTO public.array_syntax_0 VALUES ('{1,2,3}'),('{{1,2},{3,4}}')", 2))
		s := arrayReferenceSelect("SELECT a FROM public.array_syntax_0", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{1,2,3}"}, {"{{1,2},{3,4}}"}}, b.pg)
		s.bag = true
		run(s)
	})
}

func TestReferenceArrayLiteralRoundTrips(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE public.array_literals (id INT,a TEXT[][],i INTEGER[1])", 0))
		cases := []struct{ input, output string }{
			{"{}", "{}"}, {"{NULL}", "{NULL}"}, {`{"NULL"}`, `{"NULL"}`}, {`{""}`, `{""}`},
			{`[-1:0][3:4]={{1,NULL},{2,3}}`, `[-1:0][3:4]={{1,NULL},{2,3}}`},
			{`[1:2][1:2]={{1,2},{3,4}}`, `{{1,2},{3,4}}`}, {`[2]={1,7}`, `{1,7}`}, {`{{},{}}`, `{}`},
			{`[+0:+1]={1,2}`, `[0:1]={1,2}`},
			{`[-2147483648:-2147483648]={1}`, `[-2147483648:-2147483648]={1}`},
			{`[2147483646:2147483646]={1}`, `[2147483646:2147483646]={1}`},
			{`[0:0] [2:2]={{1}}`, `[0:0][2:2]={{1}}`},
			{`{{{{{{1}}}}}}`, `{{{{{{1}}}}}}`},
			{`{ alpha , "a,b", "a{b}", "a\\b", "a\"b", " a " }`, `{alpha,"a,b","a{b}","a\\b","a\"b"," a "}`},
			{`{N\ULL,\NULL,NU\LL,NUL\L,null,NULL}`, `{"NULL","NULL","NULL","NULL",NULL,NULL}`},
			{`{a\,b,a\{b\},a'b,a b}`, `{"a,b","a{b}",a'b,"a b"}`},
			{"{\"a\tb\",\"a\nb\"}", "{\"a\tb\",\"a\nb\"}"},
		}
		expected := make([][]any, 0)
		for i, tc := range cases {
			for route := range 3 {
				id := int64(i*3 + route + 1)
				s := referenceExec("INSERT INTO public.array_literals VALUES ($1,$2,'{1,NULL}')", 1, id, tc.input)
				if route == 0 {
					s = referenceExec(fmt.Sprintf("INSERT INTO public.array_literals VALUES (%d,%s,'{1,NULL}')", id, arrayReferenceQuote(tc.input)), 1)
				}
				if route == 2 {
					s = referencePrepared(s)
				}
				run(s)
				expected = append(expected, []any{id, tc.output, "{1,NULL}"})
			}
		}
		run(referenceExec("INSERT INTO public.array_literals VALUES (1000,NULL,NULL),(1001,$1,$2)", 2, nil, nil))
		expected = append(expected, []any{int64(1000), nil, nil}, []any{int64(1001), nil, nil})
		s := arrayReferenceSelect("SELECT id,a,i FROM public.array_literals ORDER BY id", []string{"id", "a", "i"}, []string{"INT4", "_TEXT", "_INT4"}, []string{"INT4", "_TEXT", "_INT4"}, expected, b.pg)
		run(s)
		run(referencePrepared(s))
	})
}

func TestReferenceArrayElementFamilies(t *testing.T) {
	cases := []struct{ declaration, name, input, output string }{
		{"BOOL", "_BOOL", "{true,no,on,NULL}", "{t,f,t,NULL}"},
		{"BYTEA", "_BYTEA", `{"\\x00ff",NULL}`, `{"\\x00ff",NULL}`},
		{"NAME", "_NAME", "{" + strings.Repeat("é", 32) + "," + strings.Repeat("a", 64) + "}", "{" + strings.Repeat("é", 31) + "," + strings.Repeat("a", 63) + "}"},
		{"INT2", "_INT2", "{-32768,32767,NULL}", "{-32768,32767,NULL}"},
		{"INT4", "_INT4", "{-2147483648,2147483647}", "{-2147483648,2147483647}"},
		{"INT8", "_INT8", "{-9223372036854775808,9223372036854775807}", "{-9223372036854775808,9223372036854775807}"},
		{"OID", "_OID", "{0,4294967295}", "{0,4294967295}"},
		{"TEXT", "_TEXT", `{NULL,"NULL",""}`, `{NULL,"NULL",""}`},
		{"CHAR(3)", "_BPCHAR", "{a,NULL}", `{"a  ",NULL}`},
		{"VARCHAR(3)", "_VARCHAR", "{abc,NULL}", "{abc,NULL}"},
		{"FLOAT4", "_FLOAT4", "{1.5,NaN,Infinity,-Infinity}", "{1.5,NaN,Infinity,-Infinity}"},
		{"FLOAT8", "_FLOAT8", "{1.5,NaN,Infinity,-Infinity}", "{1.5,NaN,Infinity,-Infinity}"},
		{"NUMERIC", "_NUMERIC", "{12.340,NaN,Infinity,-Infinity}", "{12.340,NaN,Infinity,-Infinity}"},
		{"UUID", "_UUID", "{A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11}", "{a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11}"},
		{"DATE", "_DATE", "{2024-02-29,infinity,-infinity}", "{2024-02-29,infinity,-infinity}"},
		{"TIME", "_TIME", "{12:34:56.123,NULL}", "{12:34:56.123,NULL}"},
		{"TIMETZ", "_TIMETZ", "{12:34:56+00,NULL}", "{12:34:56+00,NULL}"},
		{"TIMESTAMP", "_TIMESTAMP", `{"2024-02-29 12:34:56",infinity,-infinity}`, `{"2024-02-29 12:34:56",infinity,-infinity}`},
		{"TIMESTAMPTZ", "_TIMESTAMPTZ", `{"2024-02-29 12:34:56+00",infinity,-infinity}`, `{"2024-02-29 12:34:56+00",infinity,-infinity}`},
		{"INTERVAL", "_INTERVAL", `{"1 day 02:03:04",NULL}`, `{"1 day 02:03:04",NULL}`},
	}
	for _, tc := range cases {
		t.Run(tc.declaration, func(t *testing.T) {
			arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
				run(referenceExec("CREATE TABLE public.array_family (id INT,a "+tc.declaration+"[])", 0))
				run(referenceExec("INSERT INTO public.array_family VALUES (1,"+arrayReferenceQuote(tc.input)+")", 1))
				run(referencePrepared(referenceExec("INSERT INTO public.array_family VALUES (2,$1)", 1, tc.input)))
				s := arrayReferenceSelect("SELECT id,a FROM public.array_family ORDER BY id", []string{"id", "a"}, []string{"INT4", tc.name}, []string{"INT4", tc.name}, [][]any{{int64(1), tc.output}, {int64(2), tc.output}}, b.pg)
				run(s)
				run(referencePrepared(s))
			})
		})
	}
}
