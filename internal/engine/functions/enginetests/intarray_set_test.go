package enginetests_test

import (
	"context"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIntarraySetBinding(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION intarray")
	view := e.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"})).Installation
	for operation := sqltype.IntarrayOverlap; operation < sqltype.IntarraySetOperationCount; operation++ {
		procedure := view.MustProcedure(installation.Members[6+int(operation)])
		arrayNull := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, TypeName: parser.TypeName{Name: "integer", ArrayBounds: []int32{-1}}, Syntax: parser.PostfixCast}
		// Explicit typed literals use the real input/coercion binder, not fake formals.
		right := parser.Expr(arrayNull)
		if operation == sqltype.IntarrayPushElem || operation == sqltype.IntarrayDeleteElem || operation == sqltype.IntarrayUnionElem {
			right = parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, TypeName: parser.TypeName{Name: "integer"}, Syntax: parser.PostfixCast}
		}
		raw := enginetest.FunctionCall(procedure.Name(), arrayNull, right)
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
		b.Query.Auth, b.Params.NoParameters = &auth, true
		bound, err := b.BindFunctionProvider(raw)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(operation, err)
		}
		node, ok := bound.(functions.IntarraySet)
		if !ok || node.Operation != operation || node.Member != uint8(6+operation) || node.RoutineReference != procedure || node.Ref.Address != procedure.Address() {
			t.Fatal("selected role", bound)
		}
		selected, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil || selected != procedure {
			t.Fatal("selected opaque declaration", err)
		}
		found := false
		for _, dep := range b.Dependencies {
			if dep.Ref == node.Ref && dep.Revision == procedure.Revision() {
				found = true
			}
		}
		if !found {
			t.Fatal("missing selected dependency")
		}
		frozen, ok := b.NormalizeStoredExpression(raw).(parser.FunctionExpr)
		if !ok || !frozen.StoredProviderRoutine || frozen.StoredProviderSymbol != procedure.Display.Source {
			t.Fatal("missing stored provenance")
		}
		for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){node.Rebuild, node.Substitute} {
			next, err := rebuild(node.Info, node.Children())
			if err != nil || !reflect.DeepEqual(next, node) {
				t.Fatal("immutable rebuild", err)
			}
		}
		for _, kind := range []expr.CaptureKind{expr.CaptureFunction, expr.CaptureOperator} {
			capture, ok, err := node.Capture(kind)
			if err != nil || !ok || capture.Count != 2 || !reflect.DeepEqual(capture.Args, node.Children()) {
				t.Fatal("ordered capture", err)
			}
		}
		spec, err := node.Render()
		if err != nil || len(spec.Args) != 2 || spec.Args[0].TransportType != node.Formals[0].Usage() || spec.Args[1].TransportType != node.Formals[1].Usage() {
			t.Fatal("exact formal rendering", err)
		}
		next, changed, err := node.Retag(node.Info, node.Children(), false, expr.Retag{Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }})
		if err != nil || changed || !reflect.DeepEqual(next, node) {
			t.Fatal("identity retag", err)
		}
		counts := [2]int{}
		children := []expr.Expr{intarrayCountingOperand{Info: expr.Info{Usage: node.Formals[0].Usage()}, Value: sqltype.Null(node.Formals[0]), Calls: &counts[0]}, intarrayCountingOperand{Info: expr.Info{Usage: node.Formals[1].Usage()}, Value: sqltype.Null(node.Formals[1]), Calls: &counts[1]}}
		counted, err := node.Rebuild(node.Info, children)
		if err != nil {
			t.Fatal(err)
		}
		value, err := counted.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}})
		if err != nil || counts != [2]int{1, 1} || !value.IsNull() || value.Type() != node.Usage.Type {
			t.Fatal("once-only strict operands", counts, value, err)
		}
		leftValue, err := node.Formals[0].Input(t.Context(), "{3,1,3,2}", -1)
		if err != nil {
			t.Fatal(err)
		}
		var rightValue sqltype.Value
		if node.Formals[1].IsArray() {
			rightValue, err = node.Formals[1].Input(t.Context(), "{2,4,2}", -1)
		} else {
			rightValue, err = sqltype.Integer(node.Formals[1], 3)
		}
		if err != nil {
			t.Fatal(err)
		}
		counts = [2]int{}
		children = []expr.Expr{intarrayCountingOperand{Info: expr.Info{Usage: node.Formals[0].Usage()}, Value: leftValue, Calls: &counts[0]}, intarrayCountingOperand{Info: expr.Info{Usage: node.Formals[1].Usage()}, Value: rightValue, Calls: &counts[1]}}
		counted, err = node.Rebuild(node.Info, children)
		if err != nil {
			t.Fatal(err)
		}
		value, err = counted.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}})
		if err != nil || counts != [2]int{1, 1} || value.IsNull() || value.Type() != node.Usage.Type {
			t.Fatal("once-only populated operands", counts, value, err)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
		if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
			t.Fatal("accepted NULL lifetime", err)
		}
		for _, mode := range []string{"missing", "foreign", "epoch", "revision"} {
			checks := []catalog.ExpressionDependency{}
			for _, dep := range b.Dependencies {
				if dep.Ref.Address == procedure.Address() {
					switch mode {
					case "missing":
						continue
					case "foreign":
						dep.Ref = new(ids.Domain).Ref(dep.Ref.Epoch, dep.Ref.Address)
					case "epoch":
						dep.Ref.Epoch++
					case "revision":
						dep.Revision++
					}
				}
				checks = append(checks, dep)
			}
			evaluation.RoutineChecks = checks
			if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err == nil {
				t.Fatal("NULL rehabilitated invalid retained authority", mode)
			}
		}
		evaluation.RoutineChecks, evaluation.RoutineReferences = nil, []ids.ObjectRef{}
		if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err == nil {
			t.Fatal("empty reference owner admitted")
		}
		evaluation.RoutineReferences = []ids.ObjectRef{node.Ref}
		if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
			t.Fatal("accepted ref owner changed", err)
		}
		state := catalog.CloneState(view.State())
		state.RemoveDescriptor(procedure.Address())
		retired := auth
		retired.Catalog = e.Catalog.Read(state)
		evaluation.RoutineAuthority = &retired
		if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err == nil {
			t.Fatal("NULL rescued missing current declaration")
		}
		evaluation.RoutineAuthority = &auth
		if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
			t.Fatal("retirement probe changed accepted authority", err)
		}
		text, _ := view.LookupType(sqltype.Text)
		wrong := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.Null(text)}
		if _, err := node.Rebuild(node.Info, []expr.Expr{wrong, node.Args[1]}); err == nil {
			t.Fatal("foreign formal rebuilt")
		}
	}
}

type intarrayCountingOperand struct {
	expr.Info
	Value sqltype.Value
	Calls *int
}

func (e intarrayCountingOperand) Eval(expr.Context) (sqltype.Value, error) {
	(*e.Calls)++
	return e.Value, nil
}
func (e intarrayCountingOperand) Children() []expr.Expr { return nil }
func (e intarrayCountingOperand) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	e.Info = info
	return e, nil
}
func (e intarrayCountingOperand) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e intarrayCountingOperand) Retag(info expr.Info, children []expr.Expr, changed bool, _ expr.Retag) (expr.Expr, bool, error) {
	n, err := e.Rebuild(info, children)
	return n, changed, err
}
func (e intarrayCountingOperand) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(intarrayCountingOperand)
	return ok && e.Value == o.Value && e.Calls == o.Calls
}
func (e intarrayCountingOperand) Render() (expr.Rendering, error) {
	return expr.Rendering{Tree: "intarray counting operand"}, nil
}
