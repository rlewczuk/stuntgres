package functions

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// OptionsToTable retains only the typed argument and occurrence-owned OUT shape.
// Each invocation borrows its immutable array through an independent cursor.
type OptionsToTable struct {
	expr.Info
	Arg expr.Expr
}

func namedOptionsToTable(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Variadic || !declaredTypes(declared, []sqltype.ID{1009}) {
		return nil, nil, false
	}
	return fixedNamedInputs(b, raw, args, declared, []string{"options_array"}, 1)
}

func bindOptionsToTable(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	if _, valid := namedSlots(raw, []string{"options_array"}, 1); !valid || raw.Variadic || raw.ResolutionOnly {
		return nil, b.CallSignatureError(raw)
	}
	args, err := bindSettingArguments(b, "pg_options_to_table", raw)
	if err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if err := b.AdmitSetReturning(raw.Pos); err != nil {
		return nil, err
	}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
		{Attribute: 1, Name: "option_name", Usage: b.Type(sqltype.Text).Usage()},
		{Attribute: 2, Name: "option_value", Usage: b.Type(sqltype.Text).Usage()},
	})
	if err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Stable}, args[0].Properties())
	info, err := b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, properties), SettingRoutineProvider, "pg_options_to_table", 1009)
	if err != nil {
		return nil, err
	}
	return OptionsToTable{Info: info, Arg: args[0]}, nil
}

func (n OptionsToTable) Validate() error {
	d := n.Usage.Type
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != 1009 || d == nil || !d.IsComposite() || d.ID() != 2249 || d.CompositeLen() != 2 {
		return fmt.Errorf("stuntgres: invalid options-to-table signature")
	}
	for i, name := range [...]string{"option_name", "option_value"} {
		m := d.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != name || m.Usage.Type == nil || m.Usage.Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid options-to-table output")
		}
	}
	return nil
}
func (OptionsToTable) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: options-to-table set used as scalar")
}
func (OptionsToTable) SourceAdmission() (bool, bool) { return true, true }
func (n OptionsToTable) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(OptionsToTable)
	return ok && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n OptionsToTable) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	value, err := n.Arg.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return openOptionsSource(ctx, n.Usage.Type, value)
}

func openOptionsSource(ctx context.Context, typ *sqltype.Descriptor, value sqltype.Value) (expr.SourceCursor, error) {
	// PostgreSQL decomposes the whole array before returning its first row.
	for i := range value.ArrayLen() {
		if err := ctx.Err(); err != nil {
			return expr.SourceCursor{}, err
		}
		if value.ArrayElement(i).IsNull() {
			return expr.SourceCursor{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "null array element not allowed in this context"}
		}
	}
	if err := ctx.Err(); err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.NewSourceCursor(expr.SourceCursor{Type: typ, Value: value, Advance: nextOptionsSource}), nil
}
func nextOptionsSource(_ context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	if s.Position == s.Value.ArrayLen() {
		return sqltype.Value{}, false, nil
	}
	text, err := s.Value.ArrayElement(s.Position).Text()
	if err != nil {
		return sqltype.Value{}, false, err
	}
	s.Position++
	name, value, present := strings.Cut(text, "=")
	textType := s.Type.CompositeMember(0).Usage.Type
	fields := []sqltype.Value{sqltype.String(textType, name), sqltype.Null(textType)}
	if present {
		fields[1] = sqltype.String(s.Type.CompositeMember(1).Usage.Type, value)
	}
	row, err := sqltype.OwnedCompositeValue(s.Type, fields)
	return row, err == nil, err
}
func (n OptionsToTable) Children() []expr.Expr { return []expr.Expr{n.Arg} }
func (n OptionsToTable) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("options-to-table", children, 1); err != nil { return nil, err }
	n.Info, n.Arg = info, children[0]
	return n, n.Validate()
}
func (OptionsToTable) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (OptionsToTable) PlanningFold() expr.Fold { return expr.FoldNever }
func (OptionsToTable) StrictNull() expr.Strict { return expr.StrictAfterBare }
func (OptionsToTable) ResultNullability(expr.ResultSite) (bool, bool) { return true, true }
func (OptionsToTable) LooksLikeFunction() bool { return true }
func (n OptionsToTable) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(OptionsToTable)
	return ok && same(n.Arg, o.Arg)
}
func (n OptionsToTable) Render() (expr.Rendering, error) {
	return expr.Rendering{Tree: "OptionsToTable", Inputs: n.Children(), Form: expr.FormCall, Name: "pg_options_to_table", Quote: expr.QuoteIdentifier, Args: []expr.RenderArg{{Expr: n.Arg}}, Replay: expr.Replay{RelationOnly: true, Validate: n.Validate}}, nil
}
func (n OptionsToTable) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction { return expr.CaptureOperands{}, false, nil }
	return captureShape(1, 1)
}
func (n OptionsToTable) CaptureOperand(written int) (expr.Expr, error) { return captureAt(n.Children(), 1, written) }
