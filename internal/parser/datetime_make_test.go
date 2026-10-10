package parser

import "testing"

func TestDatetimeMakeNamedArguments(t *testing.T) {
	for _, tc := range []struct {
		query  string
		labels []string
	}{
		{"SELECT make_interval(secs:=1.5,days=>2)", []string{"secs", "days"}},
		{"SELECT pg_catalog.make_interval(1,days=>2)", []string{"", "days"}},
		{"SELECT make_timestamp(mday=>15,year=>2013,month=>7,hour=>8,min=>15,sec=>0)", []string{"mday", "year", "month", "hour", "min", "sec"}},
		{"SELECT make_interval(\"DAYS\"=>2)", []string{"DAYS"}},
		{"SELECT make_interval(1,2)", nil},
	} {
		statements, err := Parse(tc.query)
		if err != nil {
			t.Fatal(tc.query, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok {
			t.Fatal(tc.query, statements)
		}
		if len(call.ArgNames) != len(tc.labels) || len(call.Args) < len(tc.labels) {
			t.Fatal(tc, call)
		}
		for i, label := range tc.labels {
			if call.ArgNames[i].Text != label {
				t.Fatal(tc, call)
			}
			if label != "" && call.ArgNames[i].Pos.Character == 0 {
				t.Fatal("label lost location", call)
			}
		}
	}
	for _, query := range []string{"SELECT 1=>2", "SELECT 1:=2", "SELECT make_interval(days=>2) trailing garbage", "SELECT make_date(2000,1,1)::=integer"} {
		if statements, err := Parse(query); err == nil || len(statements) != 0 {
			t.Fatal("invalid suffix/operator admitted", query, statements, err)
		}
	}
	for _, query := range []string{"CREATE TABLE t(d date DEFAULT make_date(day=>1,year=>2000,month=>1))", "CREATE INDEX i ON t ((make_date(day=>1,year=>2000,month=>1)))", "INSERT INTO t VALUES(make_date(day=>1,year=>2000,month=>1))", "CREATE VIEW v AS SELECT make_interval(days=>2)"} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
}
