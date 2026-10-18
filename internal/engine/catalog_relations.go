package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type catalogUserNameExpression struct {
	expr.Info
	arg BoundExpr
}

type catalogForeignKeysExpression struct {
	expr.Info
	metadata *catalogForeignKeyValues
}

func (b *expressionBinder) bindCatalogRelation(raw parser.FunctionExpr) (BoundExpr, error) {
	// As with other OID inquiries, transform operands before overload/decorator
	// diagnostics, but retain UNKNOWN until the sole formal type is selected.
	var operand comparisonOperand
	var single [1]string
	names := single[:0]
	if len(raw.Args) == 1 {
		names = single[:]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if len(raw.Args) == 1 {
			operand = arg
		}
		if len(raw.Args) <= 100 {
			name := "unknown"
			if typ := arg.descriptor(); typ != nil {
				name = b.diagnosticTypeName(typ)
			}
			names[i] = functions.NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	scalar := name == "pg_get_userbyid"
	set := name == "pg_get_catalog_foreign_keys"
	if !visible || !scalar && !set || scalar && len(raw.Args) != 1 || set && len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	var arg BoundExpr
	if scalar {
		target := b.typ(sqltype.Oid).Usage()
		arg, err = b.finalizeOperand(operand, target.Type)
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target {
			plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
			if failure != nil {
				if diagnostic, ok := failure.(*sqlstate.Error); ok && (diagnostic.Code == "42804" || diagnostic.Code == "42846") {
					return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
				}
				return nil, failure
			}
			arg = coercionExpression{b.info(target, raw.Args[0].SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit, false}
		}
	}
	if raw.Star {
		return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	if scalar {
		properties := arg.Properties()
		properties.Volatility = max(Stable, properties.Volatility)
		info, err := b.builtinRoutineInfo(b.info(b.typ(sqltype.Name).Usage(), raw.Pos, properties), catalogRoutineProvider, name, sqltype.Oid)
		if err != nil {
			return nil, err
		}
		b.catalogSnapshot = true
		// The execution binding already carries catalog/role demand through this
		// flag, including nonquery VALUES/assignment consumers.
		b.regclassConcat = true
		return catalogUserNameExpression{info, arg}, nil
	}
	if err := b.stringSetAllowed(raw.Pos); err != nil {
		return nil, err
	}
	members := []sqltype.CompositeMember{
		{Attribute: 1, Name: "fktable", Usage: b.typ(sqltype.Regclass).Usage()},
		{Attribute: 2, Name: "fkcols", Usage: b.typ(1009).Usage()},
		{Attribute: 3, Name: "pktable", Usage: b.typ(sqltype.Regclass).Usage()},
		{Attribute: 4, Name: "pkcols", Usage: b.typ(1009).Usage()},
		{Attribute: 5, Name: "is_array", Usage: b.typ(sqltype.Bool).Usage()},
		{Attribute: 6, Name: "is_opt", Usage: b.typ(sqltype.Bool).Usage()},
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	info, err := b.builtinRoutineInfo(b.info(shape.Usage(), raw.Pos, ExprProperties{Volatility: Stable}), catalogRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	metadata, err := newCatalogForeignKeyValues(shape)
	if err != nil {
		return nil, err
	}
	b.catalogSnapshot = true
	b.regclassConcat = true
	return catalogForeignKeysExpression{info, metadata}, nil
}

func (n catalogUserNameExpression) validate() error {
	if n.arg == nil || n.arg.ResultUsage().Type == nil || n.arg.ResultUsage().Type.ID() != sqltype.Oid || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Name {
		return fmt.Errorf("stuntgres: invalid catalog role-name signature")
	}
	return nil
}

func (n catalogUserNameExpression) Eval(ctx ExprContext) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.arg.Eval(c)
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
	scope := c.liveScope()
	if scope == nil || scope.commentCatalog.state == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing catalog role-name snapshot")
	}
	engine := scope.op.session.Engine()
	if !scope.commentRoles.BelongsTo(&engine.authorizationDomain) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign catalog role-name snapshot")
	}
	role, found := scope.commentRoles.Find(engine.authorizationDomain.Principal(uint64(oid)))
	if found {
		return sqltype.String(n.Usage.Type, role.Name), nil
	}
	return sqltype.String(n.Usage.Type, "unknown (OID="+strconv.FormatUint(uint64(oid), 10)+")"), nil
}

func (n catalogForeignKeysExpression) validate() error {
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

func (n catalogForeignKeysExpression) Eval(ExprContext) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: catalog foreign-key set used as scalar")
}
