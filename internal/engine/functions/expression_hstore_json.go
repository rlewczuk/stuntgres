package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// HstoreJSON retains installed declaration identity, never caller authority.
type HstoreJSON struct {
	expr.Info
	Arg             expr.Expr
	Formal          *sqltype.Descriptor
	Loose           bool
	Ref             ids.ObjectRef
	Member          uint8
	Namespace, Name string
}

// HstoreJSONDeclaration admits only the four installed conversion roles. The
// owning extension preserves these slots across presentation and restoration.
func HstoreJSONDeclaration(member uint8) (symbol string, result sqltype.ID, loose, ok bool) {
	switch member {
	case 5:
		return "hstore_to_json", sqltype.JSON, false, true
	case 6:
		return "hstore_to_json_loose", sqltype.JSON, true, true
	case 9:
		return "hstore_to_jsonb", sqltype.JSONB, false, true
	case 10:
		return "hstore_to_jsonb_loose", sqltype.JSONB, true, true
	default:
		return "", 0, false, false
	}
}

func (e HstoreJSON) Validate() error {
	_, result, loose, admitted := HstoreJSONDeclaration(e.Member)
	if !admitted || e.Arg == nil || !e.Formal.IsHstore() || e.Arg.ResultUsage().Type != e.Formal || e.Usage.Type == nil || e.Usage.Type.ID() != result || !e.Usage.Type.Concrete() || e.Usage.Type.IsDomain() || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" || e.Loose != loose {
		return fmt.Errorf("stuntgres: invalid installed hstore JSON call")
	}
	return nil
}
func (e HstoreJSON) Children() []expr.Expr { return []expr.Expr{e.Arg} }
func (e HstoreJSON) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("HstoreJSON", children, 1); err != nil {
		return nil, err
	}
	e.Info, e.Arg = info, children[0]
	return e, e.Validate()
}
func (e HstoreJSON) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e HstoreJSON) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	formal := env.Type(e.Formal)
	changed = changed || formal != e.Formal
	e.Formal = formal
	next, err := e.Rebuild(info, children)
	return next, changed, err
}
func (e HstoreJSON) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := e.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	if c.FunctionLiteralNull(e.Arg) {
		return sqltype.Null(e.Usage.Type), nil
	}
	value, err := e.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type.ID() == sqltype.JSONB {
		return value.HstoreJSONB(ctx, e.Formal, e.Usage.Type, e.Loose)
	}
	return value.HstoreJSON(ctx, e.Formal, e.Usage.Type, e.Loose)
}
func (e HstoreJSON) ValidateStoredProvider() error        { return e.Validate() }
func (e HstoreJSON) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e HstoreJSON) PlanningFold() expr.Fold              { return expr.FoldConstant }
func (e HstoreJSON) StrictNull() expr.Strict              { return expr.StrictAfter }
func (e HstoreJSON) PreflightFold() expr.Preflight        { return expr.PreflightRegexNullOrEvaluate }
func (e HstoreJSON) LooksLikeFunction() bool              { return true }
func (e HstoreJSON) ClosureTransparent()                  {}
func (e HstoreJSON) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: e.Children(), Count: 1}, true, nil
}
func (e HstoreJSON) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(HstoreJSON)
	return ok && e.Usage.Type == o.Usage.Type && e.Formal == o.Formal && e.Loose == o.Loose && e.Ref == o.Ref && e.Member == o.Member && e.RoutineReference == o.RoutineReference && same(e.Arg, o.Arg)
}
func (e HstoreJSON) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "HstoreJSON " + e.Namespace + "." + e.Name, Inputs: e.Children(), Form: expr.FormCall, Name: e.Name, Namespace: e.Namespace, Quote: expr.QuoteIdentifier, Args: []expr.RenderArg{{Expr: e.Arg, Transport: expr.TransportCast, TransportType: e.Formal.Usage()}}, Replay: expr.Replay{QuoteNamespace: true}}, nil
}
