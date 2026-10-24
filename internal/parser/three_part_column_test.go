package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestThreePartColumnAST(t *testing.T) {
	for _, tc := range []struct {
		query   string
		parts   [3]string
		written [3]string
	}{
		{"/*é🙂*/ SELECT PUBLIC.NONCES.ID", [3]string{"public", "nonces", "id"}, [3]string{"PUBLIC", "NONCES", "ID"}},
		{`/*é🙂*/ SELECT "MixSchema"."MixTable"."MixID"`, [3]string{"MixSchema", "MixTable", "MixID"}, [3]string{`"MixSchema"`, `"MixTable"`, `"MixID"`}},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		column := statements[0].(*Select).Targets[0].(ColumnExpr)
		if len(column.Name.Parts) != 3 || column.Pos != column.Name.Parts[0].Pos {
			t.Fatal(column)
		}
		for i, part := range column.Name.Parts {
			offset := strings.Index(tc.query, tc.written[i])
			want := Position{Offset: offset, Character: utf8.RuneCountInString(tc.query[:offset]) + 1}
			if part.Text != tc.parts[i] || part.Pos != want {
				t.Fatal(part, tc.parts[i], want)
			}
		}
	}
	for _, query := range []string{
		"DELETE FROM public.nonces WHERE public.nonces.id=CAST($1 AS uuid)",
		"DELETE FROM t WHERE CAST(public.t.v AS bigint)>0 AND (abs(public.t.v))+1=2",
		"DELETE FROM t WHERE (public.t.v)::bigint=1 RETURNING public.t.v",
		"UPDATE t SET v=COALESCE(public.t.v,1) WHERE public.t.v=2",
		"INSERT INTO t VALUES(public.t.v)",
		"INSERT INTO t VALUES(length(public.t.v)) LIMIT 1",
		"CREATE TABLE t(v int DEFAULT public.t.v)",
		"SELECT current_setting(public.t.v)",
		"SELECT qa.items.id,x.v FROM qa.items CROSS JOIN LATERAL unnest(ARRAY[qa.items.v]) AS x(v) ORDER BY qa.items.id",
		"WITH removed AS (DELETE FROM qa.items WHERE (abs(CAST(qa.items.v AS int)))+1=31 AND qa.items.id=3 RETURNING qa.items.id) SELECT id FROM removed",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal("shared expression rejected", query, statements, err)
		}
		if statements, err = Parse(query + "; nonsense"); err == nil || len(statements) != 0 {
			t.Fatal("valid prefix escaped", query, statements, err)
		}
	}
	statements, err := Parse("DELETE FROM t WHERE CAST((public.t.v) AS bigint)+1=2")
	if err != nil {
		t.Fatal(err)
	}
	comparison := statements[0].(*Delete).Where.(OperatorExpr)
	arithmetic := comparison.Args[0].(OperatorExpr)
	cast := arithmetic.Args[0].(CastExpr)
	column := cast.Child.(ColumnExpr)
	if len(column.Name.Parts) != 3 || column.Name.Parts[0].Text != "public" || column.Name.Parts[1].Text != "t" || column.Name.Parts[2].Text != "v" {
		t.Fatal("composition lost column identity", column)
	}
}
