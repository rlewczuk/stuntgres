package reference_test

import (
	"errors"
	"stuntgres"
	"testing"
)

func TestReferenceLogicalOperandBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT,a BOOL,s TEXT,n NUMERIC,tm TIME)", 0))
	for _, prepared := range []bool{false, true} {
		step := referenceSelect("SELECT TRUE AND FALSE", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}})
		step.prepared = prepared
		scalarStep(t, p, step)
	}
	for _, tc := range []struct {
		q    string
		want referenceDiagnostic
	}{
		{"SELECT 1 AND TRUE FROM t", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "8"}},
		{"SELECT TRUE AND (1) FROM t", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "18"}},
		{"SELECT NOT (s) FROM t", referenceDiagnostic{Code: "42804", Message: "argument of NOT must be type boolean, not type text", Position: "13"}},
		{"SELECT FALSE AND missing FROM t", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "18"}},
		{"SELECT TRUE OR n FROM t", referenceDiagnostic{Code: "42804", Message: "argument of OR must be type boolean, not type numeric", Position: "16"}},
		{"SELECT NOT tm FROM t", referenceDiagnostic{Code: "42804", Message: "argument of NOT must be type boolean, not type time without time zone", Position: "12"}},
		{"SELECT id FROM t WHERE TRUE AND 1", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "33"}},
		{"INSERT INTO t(id) SELECT TRUE AND FALSE FROM t", referenceDiagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: "26"}},
		{"SELECT a+1 AND a FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: boolean + integer", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
	} {
		step := referenceImplicitError(tc.q, tc.want)
		step.selectRows = true
		step.errorStage = "execute"
		scalarStep(t, p, step)
		step.errorStage = "prepare"
		scalarStep(t, p, referencePrepared(step))
	}
	for _, tc := range []struct {
		q  string
		pg referenceStep
	}{
		{"SELECT id=1=TRUE FROM t", referenceImplicitError("SELECT id=1=TRUE FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "="`, Position: "12"})},
		{"INSERT INTO t(id) VALUES(TRUE AND FALSE)", referenceImplicitError("INSERT INTO t(id) VALUES(TRUE AND FALSE)", referenceDiagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: "26"})},
		{"INSERT INTO t(id) VALUES(NOT TRUE)", referenceImplicitError("INSERT INTO t(id) VALUES(NOT TRUE)", referenceDiagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: "26"})},
		{"SELECT current_setting(TRUE AND FALSE)", referenceImplicitError("SELECT current_setting(TRUE AND FALSE)", referenceDiagnostic{Code: "42883", Message: "function current_setting(boolean) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})},
		{"SELECT f(a) AND a FROM t", referenceImplicitError("SELECT f(a) AND a FROM t", referenceDiagnostic{Code: "42883", Message: "function f(boolean) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})},
	} {
		t.Run(tc.q, func(t *testing.T) {
			rows, err := p.memory.QueryContext(p.ctx, tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("native boundary exposed rows")
			}
			var d *stuntgres.Error
			if !errors.As(err, &d) || (d.Code != "0A000" && d.Code != "42601") {
				t.Fatal(tc.q, err)
			}
			held, err := p.memory.PrepareContext(p.ctx, tc.q)
			if held != nil {
				held.Close()
				t.Fatal("native boundary exposed handle")
			}
			if !errors.As(err, &d) {
				t.Fatal(err)
			}
			if p.postgres != nil {
				step := tc.pg
				step.selectRows = true
				step.errorStage = "execute"
				scalarRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
				step.errorStage = "prepare"
				scalarRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(step))
			}
		})
	}
	scalarStep(t, p, referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, nil))
}

func TestReferenceLogicalQualifiedReferences(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT,a BOOL)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1,TRUE),(2,FALSE),(3,NULL)", 3))
	for _, q := range []string{"SELECT a::BOOL AND a FROM t ORDER BY id", "SELECT t.a AND a FROM t ORDER BY id", "SELECT a AND a AS result FROM t ORDER BY id", "SELECT x.a AND y.a FROM t x JOIN t y USING(id) ORDER BY id"} {
		name := "?column?"
		if q == "SELECT a AND a AS result FROM t ORDER BY id" {
			name = "result"
		}
		step := referenceSelect(q, []string{name}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
		scalarStep(t, p, step, 0)
		scalarStep(t, p, referencePrepared(step), 0)
	}
}
