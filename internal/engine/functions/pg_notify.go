package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// NotificationRuntime appends one validated event to the statement's single
// notification-effects queue, shared with NOTIFY and tcn.
type NotificationRuntime interface {
	QueueNotification(channel, payload string) error
}

// PgNotify is the pg_catalog.pg_notify(text, text) -> void function form. It
// stages the same statement-local event NOTIFY does; publication and folding
// remain the transaction's.
type PgNotify struct {
	expr.Info
	Args [2]expr.Expr
}

func bindPgNotify(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	var operands [2]Argument
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = arg
		}
		if i < len(names) {
			name := "unknown"
			if arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if b.DefaultConsumer() {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	if !visible || len(raw.Args) != 2 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	text := b.Type(sqltype.Text).Usage()
	if !knownCallEdges(b, operands[:2], []sqltype.Usage{text, text}) {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var args [2]expr.Expr
	properties := expr.Properties{Volatility: expr.Volatile}
	for i := range operands {
		arg, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: DirectFormalArgument, Target: text})
		if err != nil {
			return nil, err
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Void).Usage(), raw.Pos, properties),
		CatalogRoutineProvider, "pg_notify", sqltype.Text, sqltype.Text)
	if err != nil {
		return nil, err
	}
	return PgNotify{Info: info, Args: args}, nil
}

func (n PgNotify) Validate() error {
	if n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Void {
		return fmt.Errorf("stuntgres: invalid pg_notify result")
	}
	for i, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid pg_notify argument %d", i)
		}
	}
	return nil
}

func (n PgNotify) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	var texts [2]string
	for i, arg := range n.Args {
		value, err := arg.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			continue
		}
		text, err := value.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		texts[i] = text
	}
	runtime, ok := ctx.(NotificationRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing notification runtime")
	}
	if err := runtime.QueueNotification(texts[0], texts[1]); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.VoidValue(n.Usage.Type), nil
}

func (PgNotify) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (PgNotify) PlanningFold() expr.Fold              { return expr.FoldNever }
func (PgNotify) StrictNull() expr.Strict              { return expr.StrictNone }
func (PgNotify) PreflightFold() expr.Preflight        { return expr.PreflightKeep }
func (PgNotify) LooksLikeFunction() bool              { return true }
func (PgNotify) ClosedDefault(func(expr.Expr) bool) bool {
	return false
}
func (PgNotify) SourceAdmission() (bool, bool) { return true, false }
func (n PgNotify) Children() []expr.Expr       { return n.Args[:] }

func (n PgNotify) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}

func (n PgNotify) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(PgNotify)
	return ok && n.ResultUsage() == o.ResultUsage()
}

func (n PgNotify) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(PgNotify)
	return ok && sameChildren(n.Args[:], o.Args[:], same)
}

func (n PgNotify) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("pg_notify", children, 2); err != nil {
		return nil, err
	}
	if children[0] == nil || children[1] == nil {
		return nil, fmt.Errorf("stuntgres: invalid pg_notify reconstruction")
	}
	n.Info = info
	n.Args = [2]expr.Expr{children[0], children[1]}
	return n, nil
}

func (n PgNotify) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	v, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := v.(PgNotify)
	return rebuilt, rebuilt.Validate()
}

func (n PgNotify) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{
		Tree: "PgNotify", Inputs: n.Args[:], Form: expr.FormCall, Name: "pg_notify",
		Args: []expr.RenderArg{{Expr: n.Args[0]}, {Expr: n.Args[1]}},
	}, nil
}

func (n PgNotify) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureShape(2, 2)
}

func (n PgNotify) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(n.Args[:], 2, written)
}

func (n PgNotify) ValidateStoredProvider() error { return n.Validate() }
