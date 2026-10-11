package reference_test

import (
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLogicalOperandBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(id INT,a BOOL,s TEXT,n NUMERIC,tm TIME)", 0))
	for _, prepared := range []bool{false, true} {
		step := fixture.Select("SELECT TRUE AND FALSE", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}})
		step.Prepared = prepared
		fixture.ScalarStep(t, p, step)
	}
	for _, tc := range []struct {
		q    string
		want fixture.Diagnostic
	}{
		{"SELECT 1 AND TRUE FROM t", fixture.Diagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "8"}},
		{"SELECT TRUE AND (1) FROM t", fixture.Diagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "18"}},
		{"SELECT NOT (s) FROM t", fixture.Diagnostic{Code: "42804", Message: "argument of NOT must be type boolean, not type text", Position: "13"}},
		{"SELECT FALSE AND missing FROM t", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "18"}},
		{"SELECT TRUE OR n FROM t", fixture.Diagnostic{Code: "42804", Message: "argument of OR must be type boolean, not type numeric", Position: "16"}},
		{"SELECT NOT tm FROM t", fixture.Diagnostic{Code: "42804", Message: "argument of NOT must be type boolean, not type time without time zone", Position: "12"}},
		{"SELECT id FROM t WHERE TRUE AND 1", fixture.Diagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "33"}},
		{"INSERT INTO t(id) SELECT TRUE AND FALSE FROM t", fixture.Diagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: "26"}},
		{"SELECT a+1 AND a FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: boolean + integer", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
	} {
		step := fixture.ImplicitError(tc.q, tc.want)
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.ScalarStep(t, p, step)
		step.ErrorStage = "prepare"
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
	for _, tc := range []struct {
		q  string
		pg fixture.Step
	}{
		{"SELECT id=1=TRUE FROM t", fixture.ImplicitError("SELECT id=1=TRUE FROM t", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "="`, Position: "12"})},
		{"INSERT INTO t(id) VALUES(TRUE AND FALSE)", fixture.ImplicitError("INSERT INTO t(id) VALUES(TRUE AND FALSE)", fixture.Diagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: "26"})},
		{"INSERT INTO t(id) VALUES(NOT TRUE)", fixture.ImplicitError("INSERT INTO t(id) VALUES(NOT TRUE)", fixture.Diagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: "26"})},
		{"SELECT current_setting(TRUE AND FALSE)", fixture.ImplicitError("SELECT current_setting(TRUE AND FALSE)", fixture.Diagnostic{Code: "42883", Message: "function current_setting(boolean) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})},
		{"SELECT f(a) AND a FROM t", fixture.ImplicitError("SELECT f(a) AND a FROM t", fixture.Diagnostic{Code: "42883", Message: "function f(boolean) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})},
	} {
		t.Run(tc.q, func(t *testing.T) {
			rows, err := p.Memory.QueryContext(p.Ctx, tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("native boundary exposed rows")
			}
			var d *stuntgres.Error
			if !errors.As(err, &d) || (d.Code != "0A000" && d.Code != "42601") {
				t.Fatal(tc.q, err)
			}
			held, err := p.Memory.PrepareContext(p.Ctx, tc.q)
			if held != nil {
				held.Close()
				t.Fatal("native boundary exposed handle")
			}
			if !errors.As(err, &d) {
				t.Fatal(err)
			}
			if p.Postgres != nil {
				step := tc.pg
				step.SelectRows = true
				step.ErrorStage = "execute"
				fixture.ScalarRunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
				step.ErrorStage = "prepare"
				fixture.ScalarRunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(step))
			}
		})
	}
	fixture.ScalarStep(t, p, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, nil))
}

func TestReferenceLogicalQualifiedReferences(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(id INT,a BOOL)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,FALSE),(3,NULL)", 3))
	for _, q := range []string{"SELECT a::BOOL AND a FROM t ORDER BY id", "SELECT t.a AND a FROM t ORDER BY id", "SELECT a AND a AS result FROM t ORDER BY id", "SELECT x.a AND y.a FROM t x JOIN t y USING(id) ORDER BY id"} {
		name := "?column?"
		if q == "SELECT a AND a AS result FROM t ORDER BY id" {
			name = "result"
		}
		step := fixture.Select(q, []string{name}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
		fixture.ScalarStep(t, p, step, 0)
		fixture.ScalarStep(t, p, fixture.Prepared(step), 0)
	}
}
