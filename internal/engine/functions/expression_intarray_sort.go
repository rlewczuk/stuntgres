package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// IntarraySort retains the selected overload and generation-exact formals.
// The inactive portion of both fixed buffers is always empty.
type IntarraySort struct {
	expr.Info
	Args            [2]expr.Expr
	Formals         [2]*sqltype.Descriptor
	Operation       sqltype.IntarraySortOperation
	Ref             ids.ObjectRef
	Member          uint8
	Namespace, Name string
}

func (e IntarraySort) Validate() error {
	n := e.Operation.Arity()
	if n == 0 || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" || int(e.Member) != 39+int(e.Operation) {
		return fmt.Errorf("stuntgres: invalid installed intarray sort call")
	}
	for i := range e.Args {
		if i >= n {
			if e.Args[i] != nil || e.Formals[i] != nil {
				return fmt.Errorf("stuntgres: nonempty inactive intarray sort argument")
			}
		} else if e.Args[i] == nil || e.Args[i].ResultUsage().Type != e.Formals[i] {
			return fmt.Errorf("stuntgres: invalid intarray sort operand")
		}
	}
	return e.Operation.Validate(e.Formals[:n], e.Usage.Type)
}

func (e IntarraySort) Children() []expr.Expr {
	n := e.Operation.Arity()
	children := make([]expr.Expr, n)
	copy(children, e.Args[:n])
	return children
}
func (e IntarraySort) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("IntarraySort", children, e.Operation.Arity()); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, [2]expr.Expr{}
	copy(e.Args[:], children)
	return e, e.Validate()
}
func (e IntarraySort) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e IntarraySort) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	for i := range e.Operation.Arity() {
		old := e.Formals[i]
		e.Formals[i] = env.Type(old)
		changed = changed || old != e.Formals[i]
	}
	next, err := e.Rebuild(info, children)
	return next, changed, err
}
func (e IntarraySort) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	n := e.Operation.Arity()
	var arguments [2]sqltype.Value
	for i := range n {
		arguments[i], err = e.Args[i].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	body, err := e.EnterRoutine(c, arguments[:n])
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	return e.Operation.Eval(ctx, arguments[:n], e.Formals[:n], e.Usage.Type)
}
func (e IntarraySort) ValidateStoredProvider() error        { return e.Validate() }
func (e IntarraySort) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e IntarraySort) PlanningFold() expr.Fold              { return expr.FoldConstant }
func (e IntarraySort) StrictNull() expr.Strict              { return expr.StrictAfter }
func (e IntarraySort) PreflightFold() expr.Preflight        { return expr.PreflightNullOrEvaluate }
func (e IntarraySort) LooksLikeFunction() bool              { return true }
func (e IntarraySort) ClosureTransparent()                  {}
func (e IntarraySort) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction && kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: e.Children(), Count: e.Operation.Arity()}, true, nil
}
func (e IntarraySort) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(IntarraySort)
	if !ok || e.Formals != o.Formals || e.Operation != o.Operation || e.Ref != o.Ref || e.Member != o.Member || e.RoutineReference != o.RoutineReference {
		return false
	}
	for i := range e.Operation.Arity() {
		if !same(e.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}
func (e IntarraySort) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, e.Operation.Arity())
	for i := range args {
		args[i] = expr.RenderArg{Expr: e.Args[i], Transport: expr.TransportCast, TransportType: e.Formals[i].Usage()}
	}
	return expr.Rendering{Tree: "IntarraySort " + e.Namespace + "." + e.Name, Inputs: e.Children(), Form: expr.FormCall, Name: e.Name, Namespace: e.Namespace, Quote: expr.QuoteIdentifier, Args: args, Replay: expr.Replay{QuoteNamespace: true}}, nil
}
func (e IntarraySort) SourceAdmission() (bool, bool) { return true, false }
func (e IntarraySort) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
