package functions

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// PgpArmorHeaders owns no invocation state. Each open validates the complete
// header region and creates an inline cursor borrowing only immutable text.
type PgpArmorHeaders struct {
	expr.Info
	Arg expr.Expr
}

func bindPgpArmorHeaders(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var single [1]Argument
	operands := single[:]
	if len(raw.Args) != 1 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, PgcryptoExtension, "25")
	if err != nil {
		return nil, err
	}
	valid := routine != nil && !raw.ResolutionOnly && !raw.Variadic && len(operands) == 1 && len(raw.ArgNames) == 0 && !raw.All && !raw.Star
	formal := b.Type(sqltype.Text).Usage()
	if valid && operands[0].Usage.Type != nil {
		_, err = sqltype.PlanCoercion(operands[0].Usage, formal, sqltype.Implicit)
		valid = err == nil
	}
	if !valid {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	arg, err := b.CoerceArgument(operands[0], CoercionRequest{Intent: DirectFormalArgument, Target: formal})
	if err != nil {
		return nil, err
	}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
		{Attribute: 1, Name: "key", Usage: formal}, {Attribute: 2, Name: "value", Usage: formal},
	})
	if err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Immutable}, arg.Properties())
	return routine.Attach(PgpArmorHeaders{Info: b.Info(shape.Usage(), raw.Pos, properties), Arg: arg})
}

func (n PgpArmorHeaders) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: pgp_armor_headers requires a source invocation")
}
func (n PgpArmorHeaders) Children() []expr.Expr { return []expr.Expr{n.Arg} }
func (n PgpArmorHeaders) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 || children[0] == nil || children[0].ResultUsage().Type == nil || children[0].ResultUsage().Type.ID() != sqltype.Text {
		return nil, fmt.Errorf("stuntgres: invalid pgp_armor_headers argument")
	}
	if err := ValidatePgpArmorShape(info.Usage); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}
func (n PgpArmorHeaders) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (PgpArmorHeaders) SourceAdmission() (bool, bool)        { return true, true }
func (PgpArmorHeaders) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (PgpArmorHeaders) PlanningFold() expr.Fold              { return expr.FoldNever }
func (PgpArmorHeaders) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (PgpArmorHeaders) LooksLikeFunction() bool              { return true }

func ValidatePgpArmorShape(usage sqltype.Usage) error {
	shape := usage.Type
	if shape == nil || shape.ID() != 2249 || !shape.IsComposite() || shape.CompositeLen() != 2 {
		return fmt.Errorf("stuntgres: invalid pgp_armor_headers OUT shape")
	}
	for i, name := range [...]string{"key", "value"} {
		m := shape.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != name || m.Usage.Type == nil || m.Usage.Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid pgp_armor_headers OUT shape")
		}
	}
	return nil
}

func (n PgpArmorHeaders) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	value, err := n.Arg.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if value.IsNull() {
		return expr.OwnedSource(n.Usage.Type, nil), nil
	}
	text, err := value.Text()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	start, stop, err := sqltype.PgcryptoArmorHeaderRegion(ctx, text)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, Value: value, Position: start, Stop: int64(stop), Advance: nextArmorHeader}), nil
}

func nextArmorHeader(_ context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if int64(cursor.Position) >= cursor.Stop {
		return sqltype.Value{}, false, nil
	}
	text, err := cursor.Value.Text()
	if err != nil {
		return sqltype.Value{}, false, err
	}
	end := cursor.Position + strings.IndexByte(text[cursor.Position:int(cursor.Stop)], '\n')
	line := strings.TrimSuffix(text[cursor.Position:end], "\r")
	colon := strings.Index(line, ": ")
	cursor.Position = end + 1
	var members [2]sqltype.Value
	members[0] = sqltype.String(cursor.Type.CompositeMember(0).Usage.Type, line[:colon])
	members[1] = sqltype.String(cursor.Type.CompositeMember(1).Usage.Type, line[colon+2:])
	value, err := sqltype.OwnedCompositeValue(cursor.Type, members[:])
	return value, err == nil, err
}
