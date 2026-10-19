package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type CommentInquiryKind uint8

const (
	ColumnDescription CommentInquiryKind = iota
	ObjectDescription
	LegacyDescription
	SharedDescription
)

type CommentInquiry struct {
	expr.Info
	Kind CommentInquiryKind
	Args []expr.Expr
}

func bindCommentInquiry(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	inquiry, err := BindCommentInquiryStructure(b, raw, visible)
	if err != nil {
		return nil, err
	}
	// Analyze every child before STRICT eliminates execution demand.
	for _, arg := range inquiry.Args {
		if literal, ok := b.ConstantValue(arg); ok && literal.IsNull() {
			typ := b.Type(sqltype.Text)
			return b.Literal(sqltype.Null(typ), raw.Pos), nil
		}
	}
	return inquiry, nil
}

// bindCommentInquiryStructure retains typed children for capture without
// evaluating them or changing the executable STRICT fold.
func BindCommentInquiryStructure(b Binder, raw parser.FunctionExpr, visible bool) (CommentInquiry, error) {
	if b.DefaultConsumer() {
		return CommentInquiry{}, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	if len(raw.Args) > 100 {
		// Argument transformation precedes PostgreSQL's global limit check.
		// Discard analyzed children; this call cannot produce an executable node.
		for _, child := range raw.Args {
			if _, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: BareProjectionArgument}); err != nil {
				return CommentInquiry{}, err
			}
		}
		return CommentInquiry{}, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind, second := ObjectDescription, sqltype.Name
	if name == "col_description" {
		kind, second = ColumnDescription, sqltype.Int4
	}
	if name == "shobj_description" {
		kind = SharedDescription
	}
	arity := 2
	if name == "obj_description" && len(raw.Args) == 1 {
		kind, arity = LegacyDescription, 1
	}
	// PostgreSQL ignores VARIADIC decoration on a matching fixed signature.
	if !visible || len(raw.Args) != arity {
		return CommentInquiry{}, b.CallSignatureError(raw)
	}
	args := make([]expr.Expr, arity)
	properties := expr.Properties{Volatility: expr.Stable}
	for i, child := range raw.Args {
		id := sqltype.Oid
		if i == 1 {
			id = second
		}
		arg, err := b.CoerceArgument(Argument{Raw: child}, CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(id).Usage()})
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && (d.Code == "42804" || d.Code == "42846") {
				return CommentInquiry{}, b.CallSignatureError(raw)
			}
			return CommentInquiry{}, err
		}
		args[i] = arg
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	formal := [2]sqltype.ID{sqltype.Oid, second}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Text).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formal[:arity]...)
	if err != nil {
		return CommentInquiry{}, err
	}
	return CommentInquiry{info, kind, args}, nil
}
func (e CommentInquiry) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := ctx
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	null := false
	for i, arg := range e.Args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
		null = null || v.IsNull()
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	runtime, err := catalogInquiryRuntime(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.CommentInquiry(e.Kind, values, e.Info, e.Usage.Type)
}
