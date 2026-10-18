package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type NullCountKind uint8

const (
	CountNulls NullCountKind = iota + 1
	CountNonnulls
)

func NullCountFunctionKind(name string) NullCountKind {
	switch name {
	case "num_nulls":
		return CountNulls
	case "num_nonnulls":
		return CountNonnulls
	default:
		return 0
	}
}

type NullCount struct {
	expr.Info
	Kind       NullCountKind
	Args       []expr.Expr
	DotBuiltin bool // Admitted field-notation fallback, retained for stored replay.
}

func (e NullCount) ValidSignature() bool {
	return e.Usage.Type != nil && e.Usage.Type.ID() == sqltype.Int4 && len(e.Args) > 0 && len(e.Args) <= 100
}
func (e NullCount) Name() string {
	switch e.Kind {
	case CountNulls:
		return "num_nulls"
	case CountNonnulls:
		return "num_nonnulls"
	}
	return ""
}
func (e NullCount) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if !e.ValidSignature() || e.Name() == "" {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid null-count expression")
	}
	var count int64
	for _, arg := range e.Args {
		handled, absent, err := c.WholeRowNullPresence(arg)
		if err != nil {
			return sqltype.Value{}, err
		}
		if handled {
			if absent == (e.Kind == CountNulls) {
				count++
			}
			continue
		}
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		usage := arg.ResultUsage()
		if usage.Type == nil || !usage.Type.Concrete() || value.Type() != usage.Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid null-count argument type")
		}
		if value.IsNull() == (e.Kind == CountNulls) {
			count++
		}
	}
	return sqltype.Integer(e.Usage.Type, count)
}

type NullCountArgumentIssue struct {
	UnsupportedArithmetic bool
	Position              parser.Position
}

func AnalyzeNullCountArgument(b ArgumentAnalyzer, raw parser.Expr) (Argument, NullCountArgumentIssue, error) {
	// Casts remain selected arguments, but cannot hide computed arithmetic.
	// Former signed literal input remains admitted through either spelling.
	argument := raw
	for {
		cast, ok := argument.(parser.CastExpr)
		if !ok {
			break
		}
		argument = cast.Child
	}
	if operator, ok := argument.(parser.OperatorExpr); ok {
		issue := NullCountArgumentIssue{UnsupportedArithmetic: true, Position: operator.Pos}
		if len(operator.Name.Parts) != 1 || (operator.Name.Parts[0].Text != "+" && operator.Name.Parts[0].Text != "-") || len(operator.Args) != 1 {
			return Argument{}, issue, nil
		}
		child := operator.Args[0]
		for {
			cast, ok := child.(parser.CastExpr)
			if !ok {
				break
			}
			child = cast.Child
		}
		literal, ok := child.(parser.LiteralExpr)
		if !ok || (literal.Value.Kind != parser.IntegerValue && literal.Value.Kind != parser.NumericValue) {
			return Argument{}, issue, nil
		}
		literal.Value.Pos = operator.Pos
		if _, direct := operator.Args[0].(parser.LiteralExpr); direct && operator.Name.Parts[0].Text == "+" {
			if _, wrapped := raw.(parser.CastExpr); !wrapped {
				raw = literal
			}
		}
	}
	bound, err := b.AnalyzeArgument(raw, AnalysisRequest{Intent: ProjectionArgument})
	return bound, NullCountArgumentIssue{}, err
}
func NullCountAllowed(visible, variadic bool, count int) bool {
	return visible && count != 0 && (!variadic || count == 1)
}
func NullCountVariadicArray(usage sqltype.Usage) bool {
	return usage.Type != nil && usage.Type.BaseType().IsArray()
}

// NullCount implements expr.Composite. It has no provider to revalidate, so Rebuild
// only takes the new metadata and children.
var _ expr.Composite = NullCount{}

func (e NullCount) Children() []expr.Expr { return e.Args }
func (e NullCount) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("NullCount", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, append([]expr.Expr(nil), children...)
	return e, nil
}
