package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestDeleteReturningDiagnostics(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT)")
	run(t, e, "INSERT INTO t VALUES(1),(2)")
	run(t, e, "CREATE TABLE z()")
	run(t, e, "CREATE TABLE rt(v TEXT)")
	run(t, e, "INSERT INTO rt VALUES(NULL)")
	for _, tc := range []struct{ q, code, message, position, hint string }{
		{"DELETE FROM t RETURNING zzzzz", "42703", `column "zzzzz" does not exist`, "25", ""},
		{"/*é*/ DELETE FROM t RETURNING zzzzz", "42703", `column "zzzzz" does not exist`, "31", ""},
		{"DELETE FROM t RETURNING old.zzzzz", "42703", "column old.zzzzz does not exist", "25", ""},
		{"DELETE FROM t RETURNING x.id", "42P01", `missing FROM-clause entry for table "x"`, "25", ""},
		{"DELETE FROM t AS d RETURNING t.id", "42P01", `invalid reference to FROM-clause entry for table "t"`, "30", `Perhaps you meant to reference the table alias "d".`},
		{"DELETE FROM t RETURNING WITH (OLD AS o) old.id", "42P01", `missing FROM-clause entry for table "old"`, "41", ""},
		{"DELETE FROM t RETURNING WITH (NEW AS n) new.id", "42P01", `missing FROM-clause entry for table "new"`, "41", ""},
		{"DELETE FROM t RETURNING WITH (OLD AS o, OLD AS p) id", "42601", "OLD cannot be specified multiple times", "41", ""},
		{"DELETE FROM t RETURNING WITH (NEW AS n, NEW AS p) id", "42601", "NEW cannot be specified multiple times", "41", ""},
		{"DELETE FROM t RETURNING WITH (OLD AS t) id", "42712", `table name "t" specified more than once`, "31", ""},
		{"DELETE FROM t RETURNING WITH (OLD AS o, NEW AS o) id", "42712", `table name "o" specified more than once`, "41", ""},
		{"DELETE FROM t WHERE old.id=1 RETURNING id", "42P01", `missing FROM-clause entry for table "old"`, "21", ""},
		{"DELETE FROM z RETURNING *", "42601", "RETURNING must have at least one column", "25", ""},
		{"DELETE FROM t RETURNING count(*)", "42803", "aggregate functions are not allowed in RETURNING", "25", ""},
		{"DELETE FROM rt RETURNING old.v::regclass,new.v::regclass", "0A000", "qualified column references are unsupported", "26", ""},
		{"DELETE FROM rt RETURNING new.v::regclass", "0A000", "qualified column references are unsupported", "26", ""},
		{"DELETE FROM rt RETURNING v::regclass", "42804", "incompatible source and destination types", "", ""},
	} {
		for _, prepare := range []bool{false, true} {
			op, cleanup, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ast := statement(t, tc.q)
			if prepare {
				_, err = e.Describe(op, ast)
			} else {
				_, err = e.Execute(op, ast, 0, nil)
			}
			cleanup()
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(tc.q, prepare, err, diagnostic, want)
			}
		}
	}
	result := run(t, e, "SELECT id FROM t ORDER BY id")
	if result.Count != 2 {
		t.Fatal("diagnostics mutated rows")
	}
	result.Rows.Close()
	result = run(t, e, "SELECT v FROM rt")
	if result.Count != 1 {
		t.Fatal("REGCLASS diagnostics mutated rows")
	}
	result.Rows.Close()
}
