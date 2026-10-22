package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestLoadGrammar(t *testing.T) {
	for _, tc := range []struct{ sql, filename string }{
		{"LOAD 'ordinary'", "ordinary"},
		{";;lOaD '';;", ""},
		{"LOAD 'quo''te'", "quo'te"},
		{`LOAD E'line\nname'`, "line\nname"},
		{`LOAD U&'caf\00E9'`, "café"},
		{"LOAD $file$literal$file$", "literal"},
		{"LOAD 'join'\n'ed'", "joined"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			stmts, err := Parse(tc.sql)
			if err != nil || len(stmts) != 1 {
				t.Fatal(stmts, err)
			}
			load, ok := stmts[0].(*Load)
			if !ok || load.Filename != tc.filename {
				t.Fatalf("decoded LOAD AST: %#v", stmts[0])
			}
		})
	}
	stmts, err := Parse("/*é*/ LOAD 'x'")
	if err != nil || len(stmts) != 1 {
		t.Fatal(stmts, err)
	}
	if load := stmts[0].(*Load); load.Pos.Character != 7 || load.Pos.Offset != 7 {
		t.Fatal("UTF8 LOAD position", load.Pos)
	}
	for _, sql := range []string{
		"CREATE TABLE load(load INT); SELECT load FROM load",
		"SELECT 1 AS load; SELECT 1 load",
		"CREATE TABLE t(v load)",
		"SELECT 'x'::load",
	} {
		if _, err := Parse(sql); err != nil {
			t.Fatalf("unreserved LOAD: %s: %v", sql, err)
		}
	}
}

func TestLoadSyntax(t *testing.T) {
	for _, sql := range []string{
		"LOAD", "LOAD missing", "LOAD $1", "LOAD 1", "LOAD ('x')",
		"LOAD 'x' garbage", "LOAD 'x' || 'y'", "PREPARE l AS LOAD 'x'",
		"SELECT 1; LOAD missing", "LOAD 'x'; LOAD missing",
	} {
		stmts, err := Parse(sql)
		var diagnostic *sqlstate.Error
		if len(stmts) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatalf("whole-input rejection: %s: %#v %v", sql, stmts, err)
		}
	}
}
