package parser

import "testing"

func TestJSONBInsertSyntax(t *testing.T) {
	for _, tc := range []struct {
		call, name string
		count      int
		variadic   bool
	}{
		{`jsonb_insert($1,$2,$3)`, "jsonb_insert", 3, false},
		{`JSONB_INSERT($1,$2,$3,$4)`, "jsonb_insert", 4, false},
		{`"jsonb_insert"($1,$2,$3)`, "jsonb_insert", 3, false},
		{`"JSONB_INSERT"($1,$2,$3)`, "JSONB_INSERT", 3, false},
		{`pg_catalog.jsonb_insert($1,$2,VARIADIC $3)`, "jsonb_insert", 3, true},
		{`jsonb_insert($1,$2,$3,VARIADIC $4)`, "jsonb_insert", 4, true},
	} {
		parsed, err := Parse("/*é*/ SELECT " + tc.call)
		if err != nil || len(parsed) != 1 {
			t.Fatal(tc, err)
		}
		call, ok := parsed[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name || len(call.Args) != tc.count || call.Variadic != tc.variadic || call.Pos.Character != 14 {
			t.Fatal(tc, call)
		}
		for i, arg := range call.Args {
			if p, ok := arg.(ParameterExpr); !ok || p.Number != i+1 {
				t.Fatal("argument transport", call)
			}
		}
	}
	for _, op := range []string{"=>", ":="} {
		parsed, err := Parse("SELECT jsonb_insert(replacement " + op + " $1,jsonb_in " + op + " $2,path " + op + " $3,insert_after " + op + " $4)")
		if err != nil {
			t.Fatal(err)
		}
		call := parsed[0].(*Select).Targets[0].(FunctionExpr)
		for i, label := range []string{"replacement", "jsonb_in", "path", "insert_after"} {
			if call.ArgNames[i].Text != label || call.Args[i].(ParameterExpr).Number != i+1 {
				t.Fatal(call)
			}
		}
	}
	for _, tc := range []struct {
		sql   string
		check func(FunctionExpr) bool
	}{
		{`SELECT jsonb_insert(DISTINCT $1,$2,$3)`, func(e FunctionExpr) bool { return e.Distinct }},
		{`SELECT jsonb_insert($1,$2,$3 ORDER BY 1)`, func(e FunctionExpr) bool { return len(e.Order) == 1 }},
		{`SELECT jsonb_insert($1,$2,$3) FILTER (WHERE true)`, func(e FunctionExpr) bool { return e.Filter != nil }},
		{`SELECT jsonb_insert($1,$2,$3) OVER ()`, func(e FunctionExpr) bool { return e.Window != nil }},
		{`SELECT jsonb_insert(*)`, func(e FunctionExpr) bool { return e.Star && len(e.Args) == 0 }},
	} {
		parsed, err := Parse(tc.sql)
		if err != nil {
			t.Fatal(tc.sql, err)
		}
		if e, ok := parsed[0].(*Select).Targets[0].(FunctionExpr); !ok || !tc.check(e) {
			t.Fatal(tc.sql, e)
		}
	}
	for _, q := range []string{
		`CREATE TABLE t(src jsonb,d jsonb DEFAULT jsonb_insert('[0]','{0}','9'),g jsonb GENERATED ALWAYS AS (jsonb_insert(src,'{0}','9',true)) STORED,CHECK(jsonb_array_length(jsonb_insert(src,'{0}','9'))>0))`,
		`CREATE INDEX ix ON t ((jsonb_insert(src,'{0}','9')))`,
		`CREATE VIEW v AS SELECT jsonb_insert(src,'{0}','9',insert_after => true) AS changed FROM t`,
		`INSERT INTO t(src) VALUES(jsonb_insert('[0]','{0}','9'))`,
		`SELECT jsonb_insert FROM t`,
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
}
