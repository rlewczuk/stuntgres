package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// HstoreConstruct owns the selected operands/formals and installed generation.
// Member is the authenticated provider role, not a mutable SQL name.
type HstoreConstruct struct {
	expr.Info
	Args            [2]expr.Expr
	Formals         [2]*sqltype.Descriptor
	Hstore          *sqltype.Descriptor
	Ref             ids.ObjectRef
	Member          uint8
	Namespace, Name string
}

func (e HstoreConstruct) Arity() int {
	if e.Member == 33 {
		return 1
	}
	return 2
}
func (e HstoreConstruct) Symbol() string {
	if e.Member < 31 || e.Member > 37 {
		return ""
	}
	return [...]string{"hstore_from_text", "hstore_from_arrays", "hstore_from_array", "hstore_fetchval", "hstore_slice_to_array", "hstore_exists", "hstore_defined"}[e.Member-31]
}
func (e HstoreConstruct) Strict() bool { return e.Member >= 33 }
func (e HstoreConstruct) Validate() error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid installed hstore selected call") }
	if e.Symbol() == "" || !e.Hstore.IsHstore() || e.Usage.Type == nil || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" {
		return invalid()
	}
	var inputs [2]sqltype.ID
	result := e.Hstore.ID()
	switch e.Member {
	case 31:
		inputs = [2]sqltype.ID{25, 25}
	case 32:
		inputs = [2]sqltype.ID{1009, 1009}
	case 33:
		inputs = [2]sqltype.ID{1009, 0}
	case 34:
		inputs, result = [2]sqltype.ID{e.Hstore.ID(), 25}, 25
	case 35:
		inputs, result = [2]sqltype.ID{e.Hstore.ID(), 1009}, 1009
	case 36, 37:
		inputs, result = [2]sqltype.ID{e.Hstore.ID(), 25}, 16
	}
	if e.Usage.Type.ID() != result || e.Member <= 33 && e.Usage.Type != e.Hstore {
		return invalid()
	}
	for i := range e.Arity() {
		formal := e.Formals[i]
		if e.Args[i] == nil || formal == nil || formal.ID() != inputs[i] || e.Args[i].ResultUsage().Type != formal || !formal.Concrete() {
			return invalid()
		}
		if e.Member >= 34 && i == 0 && formal != e.Hstore {
			return invalid()
		}
		if inputs[i] == 1009 && (!formal.IsArray() || formal.Element().ID() != sqltype.Text) {
			return invalid()
		}
	}
	if e.Arity() == 1 && (e.Args[1] != nil || e.Formals[1] != nil) {
		return invalid()
	}
	if e.Member == 31 || e.Member == 32 {
		if e.Formals[0] != e.Formals[1] {
			return invalid()
		}
	}
	if e.Member == 34 || e.Member == 35 {
		if e.Usage.Type != e.Formals[1] {
			return invalid()
		}
	}
	return nil
}
func (e HstoreConstruct) Children() []expr.Expr {
	return append([]expr.Expr(nil), e.Args[:e.Arity()]...)
}
func (e HstoreConstruct) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("HstoreConstruct", children, e.Arity()); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, e.Validate()
}
func (e HstoreConstruct) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e HstoreConstruct) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	hstore := env.Type(e.Hstore)
	changed = changed || hstore != e.Hstore
	e.Hstore = hstore
	for i := range e.Arity() {
		formal := env.Type(e.Formals[i])
		changed = changed || formal != e.Formals[i]
		e.Formals[i] = formal
	}
	next, err := e.Rebuild(info, children)
	return next, changed, err
}
func (e HstoreConstruct) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Strict() {
		for i := range e.Arity() {
			if c.FunctionLiteralNull(e.Args[i]) {
				return sqltype.Null(e.Usage.Type), nil
			}
		}
	}
	var values [2]sqltype.Value
	for i := range e.Arity() {
		values[i], err = e.Args[i].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if values[i].Type() != e.Formals[i] {
			return sqltype.Value{}, fmt.Errorf("stuntgres: foreign hstore operand generation")
		}
	}
	if e.Strict() {
		for i := range e.Arity() {
			if values[i].IsNull() {
				return sqltype.Null(e.Usage.Type), nil
			}
		}
	}
	switch e.Member {
	case 31:
		return e.Hstore.HstoreFromText(ctx, values[0], values[1])
	case 32:
		return e.Hstore.HstoreFromArrays(ctx, values[0], values[1])
	case 33:
		return e.Hstore.HstoreFromArray(ctx, values[0])
	case 34:
		return values[0].HstoreFetch(ctx, e.Hstore, values[1])
	case 35:
		return values[0].HstoreFetchArray(ctx, e.Hstore, e.Usage.Type, values[1])
	case 36, 37:
		return values[0].HstoreExists(ctx, e.Hstore, e.Usage.Type, values[1], e.Member == 37)
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid hstore operation")
}
func (e HstoreConstruct) ValidateStoredProvider() error        { return e.Validate() }
func (e HstoreConstruct) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e HstoreConstruct) PlanningFold() expr.Fold              { return expr.FoldConstant }
func (e HstoreConstruct) StrictNull() expr.Strict {
	if e.Strict() {
		return expr.StrictAfter
	}
	return expr.StrictNone
}
func (e HstoreConstruct) PreflightFold() expr.Preflight {
	if e.Strict() {
		return expr.PreflightRegexNullOrEvaluate
	}
	return expr.PreflightEvaluate
}
func (e HstoreConstruct) LooksLikeFunction() bool { return true }
func (e HstoreConstruct) ClosureTransparent()     {}
func (e HstoreConstruct) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction && kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: e.Arity()}, true, nil
}
func (e HstoreConstruct) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(HstoreConstruct)
	if !ok || e.Member != o.Member || e.Hstore != o.Hstore || e.Formals != o.Formals || e.Ref != o.Ref || e.RoutineReference != o.RoutineReference {
		return false
	}
	for i := range e.Arity() {
		if !same(e.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}
func (e HstoreConstruct) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, e.Arity())
	for i := range args {
		args[i] = expr.RenderArg{Expr: e.Args[i], Transport: expr.TransportCast, TransportType: e.Formals[i].Usage()}
	}
	return expr.Rendering{Tree: "HstoreConstruct " + e.Namespace + "." + e.Name, Inputs: e.Children(), Form: expr.FormCall, Name: e.Name, Namespace: e.Namespace, Quote: expr.QuoteIdentifier, Args: args, Replay: expr.Replay{QuoteNamespace: true}}, nil
}
