package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// IntbigIO retains an authenticated support-I/O call, not a C ABI callback.
// Only original intarray slots 4/5 can be bound to this node by the analyzer.
// Public names and the opaque routine reference never grant execution authority.
type IntbigIO struct {
	expr.Info
	Arg             expr.Expr
	Formal, Datum   *sqltype.Descriptor
	Ref             ids.ObjectRef
	Member          uint8
	Namespace, Name string
}

func (e IntbigIO) Validate() error {
	if e.Arg == nil || e.Formal == nil || e.Datum == nil || !e.Datum.IsIntbigGkey() || e.Arg.ResultUsage().Type != e.Formal || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" {
		return fmt.Errorf("stuntgres: invalid installed intbig I/O call")
	}
	if e.Member == 4 && e.Formal.ID() == 2275 && e.Usage.Type == e.Datum || e.Member == 5 && e.Formal == e.Datum && e.Usage.Type != nil && e.Usage.Type.ID() == 2275 {
		return nil
	}
	return fmt.Errorf("stuntgres: invalid installed intbig I/O signature")
}
func (e IntbigIO) Children() []expr.Expr { return []expr.Expr{e.Arg} }
func (e IntbigIO) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("IntbigIO", children, 1); err != nil {
		return nil, err
	}
	e.Info, e.Arg = info, children[0]
	e.Formal = e.Arg.ResultUsage().Type
	if e.Member == 4 {
		e.Datum = e.Usage.Type
	} else {
		e.Datum = e.Formal
	}
	return e, e.Validate()
}
func (e IntbigIO) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e IntbigIO) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	formal, datum := env.Type(e.Formal), env.Type(e.Datum)
	changed = changed || formal != e.Formal || datum != e.Datum
	e.Formal, e.Datum = formal, datum
	next, err := e.Rebuild(info, children)
	return next, changed, err
}
func (e IntbigIO) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if c.FunctionLiteralNull(e.Arg) {
		return sqltype.Null(e.Usage.Type), nil
	}
	value, err := e.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	var arguments [1]sqltype.Value
	arguments[0] = value
	body, err := e.Info.EnterRoutine(c, arguments[:])
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	// The selected SQL owner has entered exactly this I/O leaf. Preserve the
	// actual environments for any other codec work, without a second FHET.
	if e.Member == 4 {
		text, err := e.Formal.OutputWithEnvironment(ctx, value, c.FunctionOutput())
		if err != nil {
			return sqltype.Value{}, err
		}
		return e.Datum.InputEnteredTypeIO(ctx, text, -1, c.FunctionInput())
	}
	text, err := e.Datum.OutputEnteredTypeIO(ctx, value, c.FunctionOutput())
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(e.Usage.Type, text), nil
}
func (e IntbigIO) SourceAdmission() (bool, bool) { return true, false }
func (e IntbigIO) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (e IntbigIO) ValidateStoredProvider() error        { return e.Validate() }
func (e IntbigIO) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e IntbigIO) PlanningFold() expr.Fold              { return expr.FoldConstant }
func (e IntbigIO) StrictNull() expr.Strict              { return expr.StrictAfter }
func (e IntbigIO) PreflightFold() expr.Preflight        { return expr.PreflightNullOrEvaluate }
func (e IntbigIO) LooksLikeFunction() bool              { return true }
func (e IntbigIO) ClosureTransparent()                  {}
func (e IntbigIO) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction && kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: e.Children(), Count: 1}, true, nil
}
func (e IntbigIO) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(IntbigIO)
	return ok && e.Formal == o.Formal && e.Datum == o.Datum && e.Member == o.Member && e.Ref == o.Ref && e.RoutineReference == o.RoutineReference && same(e.Arg, o.Arg)
}
func (e IntbigIO) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "IntbigIO " + e.Namespace + "." + e.Name, Inputs: e.Children(), Form: expr.FormCall, Name: e.Name, Namespace: e.Namespace, Quote: expr.QuoteIdentifier, Args: []expr.RenderArg{{Expr: e.Arg, Transport: expr.TransportCast, TransportType: e.Formal.Usage()}}, Replay: expr.Replay{QuoteNamespace: true}}, nil
}
