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
	var small [2]Argument
	var operands []Argument
	var names []string
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: DirectDeferredArgument})
		if err != nil {
			return CommentInquiry{}, err
		}
		if operands != nil {
			operands[i] = arg
		}
		if names != nil {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return CommentInquiry{}, err
	}
	signatureError := func() error {
		if names == nil {
			names = make([]string, len(operands))
			for i, operand := range operands {
				names[i] = "unknown"
				if operand.Usage.Type != nil {
					names[i] = b.DiagnosticTypeName(operand.Usage.Type)
				}
				names[i] = NamedArgumentType(raw, i, names[i])
			}
		}
		return b.SignatureError(raw.Name, raw.Pos, names)
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
		return CommentInquiry{}, signatureError()
	}
	args := make([]expr.Expr, arity)
	properties := expr.Properties{Volatility: expr.Stable}
	for i, operand := range operands {
		id := sqltype.Oid
		if i == 1 {
			id = second
		}
		arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(id).Usage()})
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && (d.Code == "42804" || d.Code == "42846") {
				return CommentInquiry{}, signatureError()
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
	if err := CallDecorationError(raw); err != nil {
		return CommentInquiry{}, err
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
