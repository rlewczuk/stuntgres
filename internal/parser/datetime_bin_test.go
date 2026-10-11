package parser

import "testing"

// Syntax-only units supplement the shared public reference definitions.
func TestDatetimeBinParser(t *testing.T) {
	for _, tc := range []struct {
		sql, name string
		position  int
	}{
		{"SELECT date_bin($1,$2,$3)", "date_bin", 8},
		{"/*é*/ SELECT date_bin($1,$2,$3)", "date_bin", 14},
		{"SELECT pg_catalog.date_bin($1,$2,$3)", "date_bin", 8},
		{`SELECT "date_bin"($1,$2,$3)`, "date_bin", 8},
		{`SELECT "Date_Bin"($1,$2,$3)`, "Date_Bin", 8},
	} {
		statements, err := Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc, err)
		}
		target := statements[0].(*Select).Targets[0].(FunctionExpr)
		if target.Name.Parts[len(target.Name.Parts)-1].Text != tc.name || len(target.Args) != 3 || target.Pos.Character != tc.position {
			t.Fatal(tc, target)
		}
		for i, arg := range target.Args {
			if parameter, ok := arg.(ParameterExpr); !ok || parameter.Number != i+1 {
				t.Fatal(arg)
			}
		}
	}
	for _, q := range []string{
		"CREATE TABLE db(v timestamp DEFAULT date_bin('15 minutes',TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01'))",
		"CREATE TABLE db(v timestamp,g timestamp GENERATED ALWAYS AS (date_bin('1 day',v,TIMESTAMP '2000-01-01')) STORED,CHECK(date_bin('1 day',v,TIMESTAMP '2000-01-01')=v))",
		"CREATE VIEW db_v AS SELECT date_bin('1 day',v,TIMESTAMP '2000-01-01') FROM db",
		"CREATE INDEX db_i ON db ((date_bin('1 day',v,TIMESTAMP '2000-01-01')))",
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
	}
	if _, err := Parse("SELECT date_bin('1 day',TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01') unexpected suffix"); err == nil {
		t.Fatal("trailing suffix admitted")
	}
}
