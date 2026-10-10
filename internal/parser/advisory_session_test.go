package parser

import (
	"strings"
	"testing"
)

func TestAdvisorySessionSyntax(t *testing.T) {
	names := []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_try_advisory_lock", "pg_try_advisory_lock_shared", "pg_advisory_unlock", "pg_advisory_unlock_shared", "pg_advisory_unlock_all"}
	for _, name := range names {
		for _, args := range []string{"$1", "$1,$2"} {
			if name == "pg_advisory_unlock_all" {
				if args == "$1,$2" {
					continue
				}
				args = ""
			}
			t.Run(name+"/"+args, func(t *testing.T) {
				sql := "SELECT " + strings.ToUpper(name) + "(" + args + ") FROM t"
				ss, err := Parse(sql)
				if err != nil || len(ss) != 1 {
					t.Fatal(ss, err)
				}
				selectNode := ss[0].(*Select)
				call, ok := selectNode.Targets[0].(FunctionExpr)
				arity := 1
				if args == "" {
					arity = 0
				} else if strings.Contains(args, ",") {
					arity = 2
				}
				if !ok || call.Name.Parts[0].Text != name || call.Pos.Character != 8 || len(call.Args) != arity || selectNode.From == nil {
					t.Fatal("ordinary function shape", selectNode)
				}
				for i, arg := range call.Args {
					parameter, ok := arg.(ParameterExpr)
					if !ok || parameter.Number != i+1 {
						t.Fatal("written parameter order", arg)
					}
				}
			})
		}
	}
	for _, tc := range []struct {
		sql, name string
		arity     int
	}{
		{`SELECT pg_catalog.pg_advisory_lock(1)`, "pg_advisory_lock", 1},
		{`SELECT "PG_ADVISORY_LOCK"(1)`, "PG_ADVISORY_LOCK", 1},
		{`SELECT "pg_advisory_lock"(1,2)`, "pg_advisory_lock", 2},
		{`SELECT pg_advisory_lock()`, "pg_advisory_lock", 0},
		{`SELECT pg_advisory_lock(1,2,3)`, "pg_advisory_lock", 3},
		{"SELECT pg_advisory_lock(" + strings.Repeat("1,", 100) + "1)", "pg_advisory_lock", 101},
	} {
		ss, err := Parse(tc.sql)
		if err != nil {
			t.Fatal(tc.sql, err)
		}
		call, ok := ss[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name || len(call.Args) != tc.arity {
			t.Fatal("qualification/quoted case/diagnostic syntax", tc.sql, call)
		}
	}
	for _, tc := range []struct {
		sql      string
		variadic bool
		label    string
	}{
		{"SELECT pg_advisory_lock(key => 1)", false, "key"},
		{"SELECT pg_advisory_lock(VARIADIC '{1}'::integer[])", true, ""},
	} {
		ss, err := Parse(tc.sql)
		if err != nil {
			t.Fatal(tc.sql, err)
		}
		call := ss[0].(*Select).Targets[0].(FunctionExpr)
		if call.Variadic != tc.variadic || len(call.Args) != 1 || (tc.label != "" && (len(call.ArgNames) != 1 || call.ArgNames[0].Text != tc.label)) {
			t.Fatal("retained diagnostic operands", call)
		}
	}
	ss, err := Parse("SELECT " + strings.Join(names, ",") + " FROM t")
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		column, ok := ss[0].(*Select).Targets[i].(ColumnExpr)
		if !ok || column.Name.Parts[0].Text != name {
			t.Fatal("bare names must remain columns", ss)
		}
	}
}
