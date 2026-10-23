package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// GeometryRelation preserves selected operator authority, never function-call
// admission. Its children retain their formal transports during stored replay.
type GeometryRelation struct {
	expr.Info
	Plan      sqltype.GeometryRelationPlan
	Args      [2]expr.Expr
	Namespace string
}

func NewGeometryRelation(info expr.Info, op sqltype.GeometryRelationOperator, args [2]expr.Expr, namespace string) (GeometryRelation, error) {
	var usages [2]sqltype.Usage
	for i, arg := range args {
		if i < op.Arity() {
			if arg == nil {
				return GeometryRelation{}, fmt.Errorf("stuntgres: missing geometry relation operand")
			}
			usages[i] = arg.ResultUsage()
		} else if arg != nil {
			return GeometryRelation{}, fmt.Errorf("stuntgres: extra geometry relation operand")
		}
	}
	p, err := sqltype.BindGeometryRelation(op, info.Usage, usages)
	return GeometryRelation{info, p, args, namespace}, err
}
func (e GeometryRelation) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Children() {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.Children() {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return e.Plan.Evaluate(ctx, values)
}
func (e GeometryRelation) Children() []expr.Expr { return e.Args[:e.Plan.Operation().Arity()] }
func (e GeometryRelation) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("GeometryRelation", children, e.Plan.Operation().Arity()); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	copy(args[:], children)
	return rebuilt(NewGeometryRelation(info, e.Plan.Operation(), args, e.Namespace))
}
func (e GeometryRelation) Validate() error {
	n, err := NewGeometryRelation(e.Info, e.Plan.Operation(), e.Args, e.Namespace)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored geometry relation provider")
	}
	return nil
}
func (e GeometryRelation) ValidateStoredProvider() error        { return e.Validate() }
func (e GeometryRelation) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e GeometryRelation) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	name := e.Plan.Name()
	spec := callRendering("GeometryRelation "+name+" namespace "+e.Namespace, e.Children(), name, e.Children())
	spec.Form = expr.FormInfix
	spec.Namespace, spec.Replay.QuoteNamespace, spec.Replay.Validate = e.Namespace, true, e.Validate
	if e.Plan.Operation().Arity() == 1 {
		spec.Form = expr.FormTemplate
		spec.Pieces = []expr.Piece{expr.TextPiece("( " + name + " "), expr.ArgPiece(0), expr.TextPiece(" )")}
		namespace := e.Namespace
		if namespace == "" {
			namespace = "pg_catalog"
		}
		spec.Replay.Pieces = []expr.Piece{expr.TextPiece("( OPERATOR("), expr.IdentifierPiece(namespace), expr.TextPiece("." + name + ") "), expr.ArgPiece(0), expr.TextPiece(" )")}
		if namespace != "pg_catalog" {
			spec.Pieces = spec.Replay.Pieces
		}
	}
	for i := range spec.Args {
		// Canonical view/dump SQL uses the display cast channel; private
		// scalar replay uses TransportCast. Both must retain the formals.
		spec.Args[i].Cast = e.Args[i].ResultUsage()
		spec.Args[i].Transport = expr.TransportCast
		spec.Args[i].TransportType = e.Args[i].ResultUsage()
	}
	return spec, nil
}
func (e GeometryRelation) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(GeometryRelation)
	if !ok || e.Plan != o.Plan || e.Namespace != o.Namespace {
		return false
	}
	for i, arg := range e.Children() {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (e GeometryRelation) LooksLikeFunction() bool { return false }
func (e GeometryRelation) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("GeometryRelation", children, e.Plan.Operation().Arity()); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
func (e GeometryRelation) ClosureTransparent() {}
func (e GeometryRelation) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: e.Plan.Operation().Arity()}, true, nil
}

var _ expr.Composite = GeometryRelation{}
var _ expr.Renderable = GeometryRelation{}
