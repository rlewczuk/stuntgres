package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// HstoreRecord distinguishes the installed pseudo declaration from the exact
// effective tuple usage. It retains no invocation environment or authority.
type HstoreRecord struct {
	expr.Info
	Args            [2]expr.Expr
	Formals         [2]*sqltype.Descriptor
	Hstore          *sqltype.Descriptor
	Ref             ids.ObjectRef
	Member          uint8
	Namespace, Name string
}

func (e HstoreRecord) Arity() int {
	if e.Member == 51 {
		return 1
	}
	return 2
}
func (e HstoreRecord) Symbol() string {
	switch e.Member {
	case 51:
		return "hstore_from_record"
	case 52:
		return "hstore_populate_record"
	}
	return ""
}
func (e HstoreRecord) Validate() error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid installed hstore record call") }
	if e.Symbol() == "" || !e.Hstore.IsHstore() || e.Usage.Type == nil || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" {
		return invalid()
	}
	for i := range e.Arity() {
		if e.Args[i] == nil || e.Formals[i] == nil || e.Args[i].ResultUsage().Type != e.Formals[i] {
			return invalid()
		}
	}
	if e.Arity() == 1 {
		if e.Args[1] != nil || e.Formals[1] != nil || e.Usage.Type != e.Hstore || !e.Formals[0].BaseType().IsComposite() && !e.Formals[0].IsDeferredRecord() {
			return invalid()
		}
	} else if e.Formals[1] != e.Hstore || e.Usage != e.Args[0].ResultUsage() {
		return invalid()
	}
	return nil
}
func (e HstoreRecord) Children() []expr.Expr { return append([]expr.Expr(nil), e.Args[:e.Arity()]...) }
func (e HstoreRecord) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("HstoreRecord", children, e.Arity()); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, e.Validate()
}
func (e HstoreRecord) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e HstoreRecord) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	next := env.Type(e.Hstore)
	changed = changed || next != e.Hstore
	e.Hstore = next
	for i := range e.Arity() {
		next = env.Type(e.Formals[i])
		changed = changed || next != e.Formals[i]
		e.Formals[i] = next
	}
	node, err := e.Rebuild(info, children)
	return node, changed, err
}
func (e HstoreRecord) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i := range e.Arity() {
		values[i], err = e.Args[i].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if values[i].Type() != e.Formals[i] {
			return sqltype.Value{}, fmt.Errorf("stuntgres: foreign hstore record operand generation")
		}
	}
	if e.Arity() == 1 {
		return e.Hstore.HstoreFromRecord(ctx, values[0], c.FunctionStringOutput())
	}
	return sqltype.HstorePopulateRecord(ctx, values[0], e.Hstore, values[1], c.FunctionJSONBRecordInput())
}
func (e HstoreRecord) ValidateStoredProvider() error        { return e.Validate() }
func (e HstoreRecord) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e HstoreRecord) PlanningFold() expr.Fold              { return expr.FoldConstant }
func (e HstoreRecord) StrictNull() expr.Strict              { return expr.StrictNone }
func (e HstoreRecord) PreflightFold() expr.Preflight        { return expr.PreflightEvaluate }
func (e HstoreRecord) LooksLikeFunction() bool              { return true }
func (e HstoreRecord) ClosureTransparent()                  {}
func (e HstoreRecord) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction && kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: e.Arity()}, true, nil
}
func (e HstoreRecord) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(HstoreRecord)
	if !ok || e.Member != o.Member || e.Hstore != o.Hstore || e.Formals != o.Formals || e.Ref != o.Ref || e.RoutineReference != o.RoutineReference || e.Usage != o.Usage {
		return false
	}
	for i := range e.Arity() {
		if !same(e.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}
func (e HstoreRecord) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, e.Arity())
	for i := range args {
		args[i] = expr.RenderArg{Expr: e.Args[i], Transport: expr.TransportCast, TransportType: e.Args[i].ResultUsage()}
	}
	return expr.Rendering{Tree: "HstoreRecord " + e.Namespace + "." + e.Name, Inputs: e.Children(), Form: expr.FormCall, Name: e.Name, Namespace: e.Namespace, Quote: expr.QuoteIdentifier, Args: args, Replay: expr.Replay{QuoteNamespace: true}}, nil
}
