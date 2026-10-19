package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type CatalogUserName struct {
	expr.Info
	Arg expr.Expr
}

type CatalogForeignKeys struct {
	expr.Info
	metadata *catalogForeignKeyValues
}

func bindCatalogRelation(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	// As with other OID inquiries, transform operands before overload/decorator
	// diagnostics, but retain UNKNOWN until the sole formal type is selected.
	var operand Argument
	var single [1]string
	names := single[:0]
	if len(raw.Args) == 1 {
		names = single[:]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if len(raw.Args) == 1 {
			operand = arg
		}
		if len(raw.Args) <= 100 {
			name := "unknown"
			if typ := arg.Usage.Type; typ != nil {
				name = b.DiagnosticTypeName(typ)
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
	scalar := name == "pg_get_userbyid"
	set := name == "pg_get_catalog_foreign_keys"
	if !visible || !scalar && !set || scalar && len(raw.Args) != 1 || set && len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var arg expr.Expr
	if scalar {
		targetID := sqltype.Oid
		target := b.Type(targetID).Usage()
		arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target {
			plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
			if failure != nil {
				if diagnostic, ok := failure.(*sqlstate.Error); ok && (diagnostic.Code == "42804" || diagnostic.Code == "42846") {
					return nil, b.SignatureError(raw.Name, raw.Pos, names)
				}
				return nil, failure
			}
			arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[0], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
			if err != nil {
				return nil, err
			}
		}
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if scalar {
		properties := arg.Properties()
		properties.Volatility = max(expr.Stable, properties.Volatility)
		info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Name).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, sqltype.Oid)
		if err != nil {
			return nil, err
		}
		b.NoteCatalogSnapshot()
		// The execution binding already carries catalog/role demand through this
		// flag, including nonquery VALUES/assignment consumers.
		b.NoteCatalogText()
		return CatalogUserName{info, arg}, nil
	}
	if err := b.AdmitSetReturning(raw.Pos); err != nil {
		return nil, err
	}
	members := []sqltype.CompositeMember{
		{Attribute: 1, Name: "fktable", Usage: b.Type(sqltype.Regclass).Usage()},
		{Attribute: 2, Name: "fkcols", Usage: b.Type(1009).Usage()},
		{Attribute: 3, Name: "pktable", Usage: b.Type(sqltype.Regclass).Usage()},
		{Attribute: 4, Name: "pkcols", Usage: b.Type(1009).Usage()},
		{Attribute: 5, Name: "is_array", Usage: b.Type(sqltype.Bool).Usage()},
		{Attribute: 6, Name: "is_opt", Usage: b.Type(sqltype.Bool).Usage()},
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, expr.Properties{Volatility: expr.Stable}), CatalogRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	metadata, err := newCatalogForeignKeyValues(shape)
	if err != nil {
		return nil, err
	}
	b.NoteCatalogSnapshot()
	b.NoteCatalogText()
	return CatalogForeignKeys{info, metadata}, nil
}
func (n CatalogUserName) Validate() error {
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != sqltype.Oid || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Name {
		return fmt.Errorf("stuntgres: invalid catalog role-name signature")
	}
	return nil
}
func (n CatalogUserName) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := ctx
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	oid, err := value.OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := catalogInquiryRuntime(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.CatalogUserName(oid, n.Usage.Type)
}
func (n CatalogForeignKeys) Validate() error {
	d := n.Usage.Type
	if d == nil || !d.IsComposite() || d.ID() != 2249 || d.CompositeLen() != 6 || n.metadata == nil {
		return fmt.Errorf("stuntgres: invalid catalog foreign-key shape")
	}
	names := [6]string{"fktable", "fkcols", "pktable", "pkcols", "is_array", "is_opt"}
	ids := [6]sqltype.ID{sqltype.Regclass, 1009, sqltype.Regclass, 1009, sqltype.Bool, sqltype.Bool}
	for i := range names {
		m := d.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != names[i] || m.Usage.Type.ID() != ids[i] {
			return fmt.Errorf("stuntgres: invalid catalog foreign-key member")
		}
	}
	for i := range n.metadata.fields[0] {
		if n.metadata.fields[0][i].Type() != d.CompositeMember(i).Usage.Type {
			return fmt.Errorf("stuntgres: foreign catalog foreign-key metadata")
		}
	}
	return nil
}

func (n CatalogForeignKeys) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: catalog foreign-key set used as scalar")
}
