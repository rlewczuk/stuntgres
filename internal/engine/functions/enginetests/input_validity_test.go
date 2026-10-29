package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type ivUnitContext struct {
	*exec.Context
	ctx           context.Context
	diagnostic    *sqlstate.Error
	failure       error
	calls         int
	value, target string
}

func (c *ivUnitContext) FunctionContext() context.Context { return c.ctx }
func (c *ivUnitContext) CheckTypeInput(value, target string) (*sqlstate.Error, error) {
	c.calls++
	c.value, c.target = value, target
	return c.diagnostic, c.failure
}

func newIVUnitContext() *ivUnitContext {
	ctx := context.Background()
	return &ivUnitContext{
		Context: &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}},
		ctx:     ctx,
	}
}

type ivUnitChild struct {
	expr.Info
	value   sqltype.Value
	failure error
	order   *[]int
	ordinal int
}

func (n ivUnitChild) Eval(expr.Context) (sqltype.Value, error) {
	if n.order != nil {
		*n.order = append(*n.order, n.ordinal)
	}
	return n.value, n.failure
}

func ivBind(t *testing.T, query string) functions.InputValidity {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	raw := enginetest.Statement(t, query).(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	call, ok := node.(functions.InputValidity)
	if !ok {
		t.Fatalf("wrong node %T", node)
	}
	return call
}
func TestInputValidityProtocols(t *testing.T) {
	for _, query := range []string{"SELECT pg_input_is_valid($1,$2)", "SELECT pg_input_error_info('oops','integer')", "SELECT pg_input_error_info(type_name => 'integer',value => 'oops')"} {
		n := ivBind(t, query)
		oid := uint32(6210)
		if n.ErrorInfo {
			oid = 6211
		}
		if n.CallRoutineReference() == nil || n.CallRoutineReference().RoutineOID() != oid {
			t.Fatal("routine provenance")
		}
		if n.Properties().Volatility != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren {
			t.Fatal("fold policy")
		}
		rebuilt, err := n.Rebuild(n.Info, n.Children())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(n, rebuilt) || rebuilt.ResultUsage().Type != n.ResultUsage().Type || rebuilt.(functions.InputValidity).CallRoutineReference() != n.CallRoutineReference() {
			t.Fatal("reconstruction changed ownership")
		}
		capture, ok, err := n.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != 2 || capture.Inline != n.Args {
			t.Fatal("capture lost written operands")
		}
		rendering, err := n.Render()
		if err != nil || len(rendering.Args) != 2 || rendering.Args[0].Expr != n.Args[0] {
			t.Fatal("render lost operands", err)
		}
		if n.ValidateStoredProvider() != nil || !n.ClosedDefault(func(expr.Expr) bool { return true }) || n.ClosedDefault(func(expr.Expr) bool { return false }) {
			t.Fatal("stored/default protocol")
		}
		if !n.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
			t.Fatal("group protocol")
		}
		if _, err := n.Rebuild(n.Info, n.Children()[:1]); err == nil {
			t.Fatal("lost child admitted")
		}
		if n.ErrorInfo {
			for i, label := range []string{"message", "detail", "hint", "sql_error_code"} {
				m := n.ResultUsage().Type.CompositeMember(i)
				if m.Attribute != int32(i+1) || m.Name != label || m.Usage.Type.ID() != sqltype.Text {
					t.Fatal("record shape")
				}
			}
		} else if n.ResultUsage().Type.ID() != sqltype.Bool {
			t.Fatal("BOOL result")
		}
	}
}
func TestInputValidityNullAndDiagnosticOwnership(t *testing.T) {
	n := ivBind(t, "SELECT pg_input_error_info('42','integer')")
	ctx := newIVUnitContext()
	value, err := n.Eval(ctx)
	if err != nil || value.IsNull() || value.CompositeLen() != 4 {
		t.Fatal("valid record must not be SQL NULL", err)
	}
	for i := range 4 {
		if !value.CompositeField(i).IsNull() {
			t.Fatal("valid diagnostic field")
		}
	}
	diagnostic := &sqlstate.Error{Code: "22P02", Message: "bad input", Hint: "producer hint"}
	ctx.diagnostic = diagnostic
	value, err = n.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.CompositeField(0).Text(); got != "bad input" {
		t.Fatal(got)
	}
	if !value.CompositeField(1).IsNull() {
		t.Fatal("absent detail must be NULL")
	}
	if got, _ := value.CompositeField(3).Text(); got != "22P02" {
		t.Fatal(got)
	}
	diagnostic.Message = "changed"
	if got, _ := value.CompositeField(0).Text(); got != "bad input" {
		t.Fatal("diagnostic storage alias")
	}
	ctx.diagnostic = nil
	ctx.failure = errors.New("producer failure")
	if _, err := n.Eval(ctx); err != ctx.failure {
		t.Fatal("hard runtime error suppressed", err)
	}
	ctx.failure = nil
	text := n.Args[0].ResultUsage().Type
	order := []int{}
	n.Args = [2]expr.Expr{ivUnitChild{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, ordinal: 0}, ivUnitChild{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "missing"), order: &order, ordinal: 1}}
	before := ctx.calls
	value, err = n.Eval(ctx)
	if err != nil || !value.IsNull() || value.Type() != n.ResultUsage().Type || ctx.calls != before || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("strict NULL boundary/order", value, err)
	}
	failure := errors.New("child error")
	second := n.Args[1].(ivUnitChild)
	second.failure = failure
	n.Args[1] = second
	if _, err := n.Eval(ctx); err != failure {
		t.Fatal("NULL suppressed child error", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	ctx.ctx = canceled
	if _, err := n.Eval(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation boundary", err)
	}
}

func TestInputValidityNamedEvaluationOrder(t *testing.T) {
	n := ivBind(t, "SELECT pg_input_error_info(type_name => 'integer',value => 'oops')")
	text := n.Args[0].ResultUsage().Type
	order := []int{}
	n.Args = [2]expr.Expr{
		ivUnitChild{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "integer"), order: &order, ordinal: 0},
		ivUnitChild{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "oops"), order: &order, ordinal: 1},
	}
	c := newIVUnitContext()
	if _, err := n.Eval(c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []int{1, 0}) || c.value != "oops" || c.target != "integer" {
		t.Fatal("named calls must evaluate formals while retaining written children", order, c.value, c.target)
	}
	captured, _, _ := n.Capture(expr.CaptureFunction)
	if !reflect.DeepEqual(captured.Inline, n.Args) {
		t.Fatal("capture reordered operand occurrences")
	}
}

func TestInputValidityScalarSource(t *testing.T) {
	for _, query := range []string{"SELECT pg_input_error_info('42','integer')", "SELECT pg_input_error_info(NULL,'integer')", "SELECT pg_input_is_valid('42','integer')"} {
		n := ivBind(t, query)
		admitted, set := n.SourceAdmission()
		if !admitted || set {
			t.Fatal("scalar source cardinality")
		}
		c := newIVUnitContext()
		cursor, err := n.OpenSource(c)
		if err != nil {
			t.Fatal(err)
		}
		value, present, err := cursor.Next(context.Background())
		if err != nil || !present || value.Type() != n.ResultUsage().Type {
			t.Fatal("scalar source lost typed row", value, present, err)
		}
		strict := query == "SELECT pg_input_error_info(NULL,'integer')"
		if value.IsNull() != strict {
			t.Fatal("whole NULL versus successful record", value)
		}
		if n.ErrorInfo && !strict && value.CompositeLen() != 4 {
			t.Fatal("record source shape")
		}
		if _, present, err := cursor.Next(context.Background()); err != nil || present {
			t.Fatal("scalar source emitted more than one row", present, err)
		}
		cursor.Close()
	}
}
