package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ListeningChannels is an invocation-time snapshot recipe, not a session handle.
type ListeningChannels struct {
	expr.Info
}

// NotificationQueueUsage reads the executing cluster's pending page fraction.
type NotificationQueueUsage struct {
	expr.Info
}

func bindNotificationInfo(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) <= 100 && len(raw.Args) != 0 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
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
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	listening := name == "pg_listening_channels"
	if !visible || !listening && name != "pg_notification_queue_usage" || len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	result, volatility := sqltype.Float8, expr.Volatile
	if listening {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
		result, volatility = sqltype.Text, expr.Stable
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, expr.Properties{Volatility: volatility}), CatalogRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	if listening {
		return ListeningChannels{info}, nil
	}
	return NotificationQueueUsage{info}, nil
}

func (n ListeningChannels) Validate() error {
	if n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Text {
		return fmt.Errorf("stuntgres: invalid listening channels signature")
	}
	return nil
}

func (n NotificationQueueUsage) Validate() error {
	if n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Float8 {
		return fmt.Errorf("stuntgres: invalid notification queue usage signature")
	}
	return nil
}

func (ListeningChannels) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: listening channel set used as scalar")
}

func (n ListeningChannels) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return expr.SourceCursor{}, err
	}
	if err := n.Validate(); err != nil {
		return expr.SourceCursor{}, err
	}
	runtime, err := sessionInquiryRuntime(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	values, err := runtime.ListeningChannels(n.Usage.Type)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(n.Usage.Type, values), nil
}

func (n NotificationQueueUsage) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := sessionInquiryRuntime(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	usage, err := runtime.NotificationQueueUsage()
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Float(n.Usage.Type, usage)
}

func (n NotificationQueueUsage) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
