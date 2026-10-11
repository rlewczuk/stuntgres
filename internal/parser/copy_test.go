package parser

import (
	"testing"
)

func TestCopyParser(t *testing.T) {
	for _, tc := range []struct {
		sql      string
		from     bool
		endpoint CopyEndpoint
		options  int
	}{
		{"COPY t FROM STDIN", true, CopyStdin, 0},
		{"COPY t(a,b) TO STDOUT WITH(FORMAT text,HEADER,DELIMITER '|',NULL 'NIL')", false, CopyStdout, 4},
		{"COPY (SELECT 1 AS id) TO STDOUT WITH(HEADER true)", false, CopyStdout, 1},
		{"COPY t FROM '/not-opened'", true, CopyFile, 0},
		{"COPY t TO PROGRAM 'not-executed'", false, CopyProgram, 0},
		{"COPY t FROM STDIN DELIMITER '|' NULL 'NIL'", true, CopyStdin, 2},
		{"COPY t FROM STDIN WITH(HEADER true,HEADER false)", true, CopyStdin, 2},
	} {
		statements, err := Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.sql, statements, err)
		}
		copy, ok := statements[0].(*Copy)
		if !ok || copy.From != tc.from || copy.Endpoint != tc.endpoint || len(copy.Options) != tc.options {
			t.Fatalf("%s AST=%#v", tc.sql, copy)
		}
		if copy.Pos.Character != 1 {
			t.Fatal("COPY position", copy.Pos)
		}
		for _, option := range copy.Options {
			if option.Pos.Character == 0 {
				t.Fatal("option lost position", option)
			}
		}
	}
	for _, q := range []string{"COPY t TO STDOUT garbage", "COPY t FROM STDIN WITH(HEADER) garbage", "COPY t FROM STDOUT", "COPY (SELECT 1) FROM STDIN"} {
		statements, err := Parse(q)
		if err == nil || statements != nil {
			t.Fatalf("invalid whole COPY accepted %q %#v %v", q, statements, err)
		}
	}
	statements, err := Parse("/* é */ COPY t FROM STDIN WITH(HEADER true, HEADER false)")
	if err != nil {
		t.Fatal(err)
	}
	copy := statements[0].(*Copy)
	if copy.Pos.Character != 9 || copy.Options[1].Pos.Character != 45 {
		t.Fatalf("character COPY positions=%#v", copy)
	}
	for _, q := range []string{"CREATE TABLE copy(copy int)", "SELECT copy FROM copy"} {
		if _, err := Parse(q); err != nil {
			t.Fatal("unreserved COPY lost identifier legality", q, err)
		}
	}
}
