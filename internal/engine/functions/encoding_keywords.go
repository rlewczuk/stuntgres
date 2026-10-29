package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// EncodingNameCall retains the selected direction, not a runtime name lookup.
type EncodingNameCall struct {
	expr.Info
	Arg expr.Expr
	ToID bool
}

type Keywords struct { expr.Info }

func bindEncodingKeywords(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var single [1]string
	names := single[:0]
	if len(raw.Args) == 1 { names = single[:] } else if len(raw.Args) <= 100 { names = make([]string, len(raw.Args)) }
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		if len(raw.Args) == 1 { operand = arg }
		if len(raw.Args) <= 100 {
			name := "unknown"
			if arg.Usage.Type != nil { name = b.DiagnosticTypeName(arg.Usage.Type) }
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 { return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos) }
	if err := ValidateCallSyntax(b, raw); err != nil { return nil, err }
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil { return nil, err }
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	set := name == "pg_get_keywords"
	toID := name == "pg_char_to_encoding"
	if !visible || !set && !toID && name != "pg_encoding_to_char" || set && len(raw.Args) != 0 || !set && len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var arg expr.Expr
	formal, result := sqltype.Int4, sqltype.Name
	if toID { formal, result = sqltype.Name, sqltype.Int4 }
	if !set {
		arg, err = coerceInquiryFormal(b, operand, raw.Args[0], b.Type(formal).Usage(), func() error {
			return b.SignatureError(raw.Name, raw.Pos, names)
		})
		if err != nil { return nil, err }
	}
	if raw.Star { return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos) }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	if !set {
		properties := arg.Properties()
		properties.Volatility = max(expr.Stable, properties.Volatility)
		info, err := b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formal)
		if err != nil { return nil, err }
		return EncodingNameCall{info, arg, toID}, nil
	}
	if err := b.AdmitSetReturning(raw.Pos); err != nil { return nil, err }
	members := []sqltype.CompositeMember{
		{Attribute: 1, Name: "word", Usage: b.Type(sqltype.Text).Usage()},
		{Attribute: 2, Name: "catcode", Usage: b.Type(sqltype.Char).Usage()},
		{Attribute: 3, Name: "barelabel", Usage: b.Type(sqltype.Bool).Usage()},
		{Attribute: 4, Name: "catdesc", Usage: b.Type(sqltype.Text).Usage()},
		{Attribute: 5, Name: "baredesc", Usage: b.Type(sqltype.Text).Usage()},
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil { return nil, err }
	info, err := b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, expr.Properties{Volatility: expr.Stable}), CatalogRoutineProvider, name)
	if err != nil { return nil, err }
	return Keywords{info}, nil
}

func (n EncodingNameCall) Validate() error {
	formal, result := sqltype.Int4, sqltype.Name
	if n.ToID { formal, result = sqltype.Name, sqltype.Int4 }
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != formal || n.Usage.Type == nil || n.Usage.Type.ID() != result { return fmt.Errorf("stuntgres: invalid encoding-name signature") }
	return nil
}
func (n EncodingNameCall) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil { return sqltype.Value{}, err }
	v, err := n.Arg.Eval(ctx)
	if err != nil { return sqltype.Value{}, err }
	if v.IsNull() { return sqltype.Null(n.Usage.Type), nil }
	if n.ToID {
		text, err := v.Text()
		if err != nil { return sqltype.Value{}, err }
		return sqltype.Integer(n.Usage.Type, int64(sqltype.EncodingID(text)))
	}
	id, err := v.Integer()
	if err != nil { return sqltype.Value{}, err }
	return sqltype.String(n.Usage.Type, sqltype.EncodingName(int(id))), nil
}
func (n Keywords) Validate() error {
	d := n.Usage.Type
	if d == nil || d.ID() != 2249 || !d.IsComposite() || d.CompositeLen() != 5 { return fmt.Errorf("stuntgres: invalid keyword result shape") }
	names := [5]string{"word", "catcode", "barelabel", "catdesc", "baredesc"}
	ids := [5]sqltype.ID{sqltype.Text, sqltype.Char, sqltype.Bool, sqltype.Text, sqltype.Text}
	for i := range names {
		m := d.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != names[i] || m.Usage.Type == nil || m.Usage.Type.ID() != ids[i] { return fmt.Errorf("stuntgres: invalid keyword result member") }
	}
	return nil
}
func (Keywords) Eval(expr.Context) (sqltype.Value, error) { return sqltype.Value{}, fmt.Errorf("stuntgres: keyword set used as scalar") }
