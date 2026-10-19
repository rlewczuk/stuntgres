package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// jsonbPathArguments evaluates supplied children once in written order, then
// the genuine omitted defaults. Strictness belongs to the typed consumer.
func jsonbPathArguments(c expr.Context, args [4]expr.Expr, count int, slots [4]uint8) ([4]sqltype.Value, error) {
	var values [4]sqltype.Value
	var supplied [4]bool
	for i := range count {
		slot := slots[i]
		value, err := args[slot].Eval(c)
		if err != nil {
			return values, err
		}
		values[slot], supplied[slot] = value, true
	}
	for i := range 4 {
		if supplied[i] {
			continue
		}
		value, err := args[i].Eval(c)
		if err != nil {
			return values, err
		}
		values[i] = value
	}
	return values, nil
}

// JSONBPathExists retains immutable executable formals and written presentation.
type JSONBPathExists struct {
	expr.Info
	Plan         sqltype.JSONBPathExistsPlan
	Args         [4]expr.Expr
	WrittenCount int
	WrittenSlots [4]uint8
	Labels       [4]string
	Variadic     bool
}

func (e JSONBPathExists) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	values, err := jsonbPathArguments(c, e.Args, e.WrittenCount, e.WrittenSlots)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Plan.Evaluate(ctx, values[:])
}
func (e JSONBPathExists) Reconstruct(info expr.Info, args [4]expr.Expr) (JSONBPathExists, error) {
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathExists(info.Usage, usages[:]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONBPathExists implements expr.Composite.
var _ expr.Composite = JSONBPathExists{}

func (e JSONBPathExists) Children() []expr.Expr { return e.Args[:len(e.Args)] }
func (e JSONBPathExists) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBPathExists", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}

// JSONBPathMatch retains immutable executable formals and written presentation.
type JSONBPathMatch struct {
	expr.Info
	Plan         sqltype.JSONBPathMatchPlan
	Args         [4]expr.Expr
	WrittenCount int
	WrittenSlots [4]uint8
	Labels       [4]string
	Variadic     bool
}

func (e JSONBPathMatch) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	values, err := jsonbPathArguments(c, e.Args, e.WrittenCount, e.WrittenSlots)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Plan.Evaluate(ctx, values[:])
}
func (e JSONBPathMatch) Reconstruct(info expr.Info, args [4]expr.Expr) (JSONBPathMatch, error) {
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathMatch(info.Usage, usages[:]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONBPathMatch implements expr.Composite.
var _ expr.Composite = JSONBPathMatch{}

func (e JSONBPathMatch) Children() []expr.Expr { return e.Args[:len(e.Args)] }
func (e JSONBPathMatch) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBPathMatch", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}

// JSONBPathQueryFirst retains immutable executable formals and written presentation.
type JSONBPathQueryFirst struct {
	expr.Info
	Plan         sqltype.JSONBPathQueryFirstPlan
	Args         [4]expr.Expr
	WrittenCount int
	WrittenSlots [4]uint8
	Labels       [4]string
	Variadic     bool
}

func (e JSONBPathQueryFirst) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	values, err := jsonbPathArguments(c, e.Args, e.WrittenCount, e.WrittenSlots)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Plan.Evaluate(ctx, values[:])
}
func (e JSONBPathQueryFirst) Reconstruct(info expr.Info, args [4]expr.Expr) (JSONBPathQueryFirst, error) {
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathQueryFirst(info.Usage, usages[:]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONBPathQueryFirst implements expr.Composite.
var _ expr.Composite = JSONBPathQueryFirst{}

func (e JSONBPathQueryFirst) Children() []expr.Expr { return e.Args[:len(e.Args)] }
func (e JSONBPathQueryFirst) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBPathQueryFirst", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}

// JSONBPathQuery retains immutable executable formals and written presentation.
type JSONBPathQuery struct {
	expr.Info
	Plan         sqltype.JSONBPathQueryPlan
	Args         [4]expr.Expr
	WrittenCount int
	WrittenSlots [4]uint8
	Labels       [4]string
	Variadic     bool
}

func (e JSONBPathQuery) EvalSet(c expr.Context) ([]sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return nil, err
	}
	values, err := jsonbPathArguments(c, e.Args, e.WrittenCount, e.WrittenSlots)
	if err != nil {
		return nil, err
	}
	return e.Plan.Evaluate(ctx, values[:])
}
func (e JSONBPathQuery) Reconstruct(info expr.Info, args [4]expr.Expr) (JSONBPathQuery, error) {
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathQuery(info.Usage, usages[:]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONBPathQuery implements expr.Composite.
var _ expr.Composite = JSONBPathQuery{}

func (e JSONBPathQuery) Children() []expr.Expr { return e.Args[:len(e.Args)] }
func (e JSONBPathQuery) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBPathQuery", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}

func (e JSONBPathQuery) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSONPath query set call")
}

func (e JSONBPathExists) IntrinsicVolatility() expr.Volatility     { return expr.Immutable }
func (e JSONBPathMatch) IntrinsicVolatility() expr.Volatility      { return expr.Immutable }
func (e JSONBPathQueryFirst) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e JSONBPathQuery) IntrinsicVolatility() expr.Volatility      { return expr.Immutable }

func (e JSONBPathQuery) PlanningFold() expr.Fold { return expr.FoldNever }

func (e JSONBPathExists) ValidateStoredProvider() error     { return nil }
func (e JSONBPathMatch) ValidateStoredProvider() error      { return nil }
func (e JSONBPathQueryFirst) ValidateStoredProvider() error { return nil }

var _ expr.Renderable = JSONBPathExists{}

// Render implements expr.Renderable.
func (e JSONBPathExists) Render() (expr.Rendering, error) {
	return jsonbPathQueryRendering(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, "jsonb_path_exists")
}

var _ expr.Renderable = JSONBPathMatch{}

// Render implements expr.Renderable.
func (e JSONBPathMatch) Render() (expr.Rendering, error) {
	return jsonbPathQueryRendering(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, "jsonb_path_match")
}

var _ expr.Renderable = JSONBPathQueryFirst{}

// Render implements expr.Renderable.
func (e JSONBPathQueryFirst) Render() (expr.Rendering, error) {
	return jsonbPathQueryRendering(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, "jsonb_path_query_first")
}

var _ expr.Renderable = JSONBPathQuery{}

// Render implements expr.Renderable.
func (e JSONBPathQuery) Render() (expr.Rendering, error) {
	spec, err := jsonbPathQueryRendering(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, "jsonb_path_query")
	spec.Replay.Validate = unsupportedReplay
	return spec, err
}

func jsonbPathQueryRendering(args [4]expr.Expr, count int, slots [4]uint8, labels [4]string, variadic bool, name string) (expr.Rendering, error) {
	if count < 2 || count > 4 {
		return expr.Rendering{}, fmt.Errorf("invalid stored JSONPath query")
	}
	written, err := writtenArgs(args[:], slots[:count], labels[:], 4, true, "invalid stored JSONPath query argument")
	if err != nil {
		return expr.Rendering{}, err
	}
	return writtenCall("JSONPath query", args[:], name, written, variadic), nil
}

// SameGroup implements expr.GroupIdentity.
func (e JSONBPathExists) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(JSONBPathExists)
	// Redundant VARIADIC is presentation only for this nonvariadic routine.
	if !ok || e.WrittenCount != o.WrittenCount || e.WrittenSlots != o.WrittenSlots || e.Labels != o.Labels {
		return false
	}
	for i, arg := range e.Args[:] {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

// SameGroup implements expr.GroupIdentity.
func (e JSONBPathMatch) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(JSONBPathMatch)
	// Redundant VARIADIC is presentation only for this nonvariadic routine.
	if !ok || e.WrittenCount != o.WrittenCount || e.WrittenSlots != o.WrittenSlots || e.Labels != o.Labels {
		return false
	}
	for i, arg := range e.Args[:] {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

// SameGroup implements expr.GroupIdentity.
func (e JSONBPathQueryFirst) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(JSONBPathQueryFirst)
	// Redundant VARIADIC is presentation only for this nonvariadic routine.
	if !ok || e.WrittenCount != o.WrittenCount || e.WrittenSlots != o.WrittenSlots || e.Labels != o.Labels {
		return false
	}
	for i, arg := range e.Args[:] {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

// SameGroup implements expr.GroupIdentity.
func (e JSONBPathQuery) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(JSONBPathQuery)
	// Redundant VARIADIC is presentation only for this nonvariadic routine.
	if !ok || e.WrittenCount != o.WrittenCount || e.WrittenSlots != o.WrittenSlots || e.Labels != o.Labels {
		return false
	}
	for i, arg := range e.Args[:] {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

// LooksLikeFunction implements expr.FunctionSyntax.
func (e JSONBPathExists) LooksLikeFunction() bool { return true }

// LooksLikeFunction implements expr.FunctionSyntax.
func (e JSONBPathMatch) LooksLikeFunction() bool { return true }

// LooksLikeFunction implements expr.FunctionSyntax.
func (e JSONBPathQueryFirst) LooksLikeFunction() bool { return true }

// Substitute implements expr.Substitutable.
func (e JSONBPathExists) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBPathExists", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}

// Substitute implements expr.Substitutable.
func (e JSONBPathMatch) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBPathMatch", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}

// Substitute implements expr.Substitutable.
func (e JSONBPathQueryFirst) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBPathQueryFirst", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}

// Substitute implements expr.Substitutable.
func (e JSONBPathQuery) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBPathQuery", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}

// ClosureTransparent implements expr.ClosureTransparent.
func (e JSONBPathExists) ClosureTransparent() {}

// ClosureTransparent implements expr.ClosureTransparent.
func (e JSONBPathMatch) ClosureTransparent() {}

// ClosureTransparent implements expr.ClosureTransparent.
func (e JSONBPathQueryFirst) ClosureTransparent() {}

func (e JSONBPathExists) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureWritten(e.Args[:], e.WrittenSlots[:], e.WrittenCount, len(e.Args))
}

func (e JSONBPathExists) CaptureOperand(written int) (expr.Expr, error) {
	return captureWrittenAt(e.Args[:], e.WrittenSlots[:], e.WrittenCount, len(e.Args), written)
}

func (e JSONBPathMatch) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureWritten(e.Args[:], e.WrittenSlots[:], e.WrittenCount, len(e.Args))
}

func (e JSONBPathMatch) CaptureOperand(written int) (expr.Expr, error) {
	return captureWrittenAt(e.Args[:], e.WrittenSlots[:], e.WrittenCount, len(e.Args), written)
}

func (e JSONBPathQueryFirst) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureWritten(e.Args[:], e.WrittenSlots[:], e.WrittenCount, len(e.Args))
}

func (e JSONBPathQueryFirst) CaptureOperand(written int) (expr.Expr, error) {
	return captureWrittenAt(e.Args[:], e.WrittenSlots[:], e.WrittenCount, len(e.Args), written)
}

func (e JSONBPathQuery) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureWritten(e.Args[:], e.WrittenSlots[:], e.WrittenCount, len(e.Args))
}

func (e JSONBPathQuery) CaptureOperand(written int) (expr.Expr, error) {
	return captureWrittenAt(e.Args[:], e.WrittenSlots[:], e.WrittenCount, len(e.Args), written)
}
