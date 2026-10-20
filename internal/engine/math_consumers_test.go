package engine

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type mathObservedArgument struct {
	expr.Info
	value sqltype.Value
	order *[]int
	index int
	err   error
}

func (e mathObservedArgument) Eval(expr.Context) (sqltype.Value, error) {
	*e.order = append(*e.order, e.index)
	return e.value, e.err
}

func TestMathTraversal(t *testing.T) {
	var numeric, int4 *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Numeric {
			numeric = d
		}
		if d.ID() == sqltype.Int4 {
			int4 = d
		}
	}
	mathPlan, err := sqltype.BindMath("round", numeric.Usage(), numeric.Usage(), int4.Usage())
	if err != nil {
		t.Fatal(err)
	}
	one, _ := sqltype.Integer(int4, 1)
	// Immutable child work is simplified before strict literal-NULL folding.
	// This is not runtime nullable-column suppression or Prepare evaluation.
	modPlan, err := sqltype.BindMath("mod", int4.Usage(), int4.Usage(), int4.Usage())
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := sqltype.Integer(int4, 0)
	mod := functions.Math{Info: expr.Info{Usage: int4.Usage()}, Plan: modPlan, Args: []expr.Expr{
		plan.LiteralExpr{Info: expr.Info{Usage: int4.Usage()}, Value: one},
		plan.LiteralExpr{Info: expr.Info{Usage: int4.Usage()}, Value: zero},
	}}
	expr := functions.Math{Info: expr.Info{Usage: numeric.Usage()}, Plan: mathPlan, Args: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: numeric.Usage()}, Value: sqltype.Null(numeric)}, mod}}
	_, _, err = simplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, expr, false)
	var diagnostic interface{ SQLState() string }
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "22012" {
		t.Fatal("immutable sibling before strict NULL", err)
	}
	// Query/DML preflight must reach the same policy without a CASE wrapper,
	// including the selected-call singleton source used by public diagnostics.
	_, _, err = preflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, expr)
	diagnostic = nil
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "22012" {
		t.Fatal("math execution preflight bypassed immutable simplification", err)
	}
}

func TestMathConsumers(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE m(n NUMERIC)")
	run(t, e, "INSERT INTO m VALUES(1.25),(1.26),(1.24),(-1.25)")
	got := mathTuples(t, run(t, e, "SELECT round(n,1),count(*) FROM m GROUP BY 1"))
	counts := make(map[string]int64)
	for _, row := range got {
		counts[row[0].(string)] = row[1].(int64)
	}
	if !reflect.DeepEqual(counts, map[string]int64{"1.3": 2, "1.2": 1, "-1.3": 1}) || len(got) != 3 {
		t.Fatal(got)
	}
	got = mathTuples(t, run(t, e, "SELECT sign(count(*)),round(count(*),1) FROM m"))
	if !reflect.DeepEqual(got, [][]any{{float64(1), "4.0"}}) {
		t.Fatal(got)
	}
	for _, temporary := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-types", true: "retained-types"}[temporary], func(t *testing.T) {
			source := New()
			run(t, source, "CREATE TABLE s(id INT,n NUMERIC)")
			run(t, source, "INSERT INTO s VALUES(1,8.4100)")
			run(t, source, "CREATE VIEW v AS SELECT round(n,1) AS r,trim_scale(n) AS t,mod(n,4.0) AS m,scale(n) AS s FROM s")
			run(t, source, "CREATE VIEW null_math_view AS SELECT round(NULL,mod(id,0)) AS result FROM s")
			run(t, source, "CREATE VIEW constant_null_math_view AS SELECT round(NULL,mod(1,0)) AS result FROM s")
			engineMathViewWitnesses(t, source)
			var dump bytes.Buffer
			if err := source.Dump(context.Background(), &dump); err != nil {
				t.Fatal(err)
			}
			target := New()
			if temporary {
				op, done := roleOperation(t, target, "postgres")
				defer done()
				roleExecute(t, target, op, "CREATE TEMPORARY SEQUENCE retained")
			}
			if err := target.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if got := mathTuples(t, run(t, target, "SELECT r,t,m,s FROM v")); !reflect.DeepEqual(got, [][]any{{"8.4", "8.41", "0.4100", int64(4)}}) {
				t.Fatal(got)
			}
			engineMathViewWitnesses(t, target)
		})
	}
}

func engineMathViewWitnesses(t *testing.T, e *Engine) {
	t.Helper()
	if got := mathTuples(t, run(t, e, "SELECT result FROM null_math_view")); !reflect.DeepEqual(got, [][]any{{nil}}) {
		t.Fatal("row-dependent NULL stored view", got)
	}
	_, err := testExecute(e, context.Background(), statement(t, "SELECT result FROM constant_null_math_view"), nil)
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal("constant-error NULL stored view", got, err)
	}
}

func mathTuples(t *testing.T, result Result) [][]any {
	t.Helper()
	cursor := independentFinalRows(result.Rows)
	defer cursor.Close()
	var rows [][]any
	values := make([]sqltype.Value, len(cursor.Schema()))
	for {
		present, err := cursor.Next(context.Background(), values)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		row := make([]any, len(values))
		for i, v := range values {
			if v.IsNull() {
				continue
			}
			switch v.Type().ID() {
			case sqltype.Numeric:
				row[i], err = v.Type().Output(context.Background(), v)
			case sqltype.Float8:
				row[i], err = v.Float()
			default:
				row[i], err = v.Integer()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		rows = append(rows, row)
	}
	return rows
}
