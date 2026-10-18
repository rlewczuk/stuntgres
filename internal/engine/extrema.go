package engine

import (
	"stuntgres/internal/engine/functions"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindExtrema(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	var singleType [1]string
	types := singleType[:]
	if len(raw.Args) != 1 {
		types = make([]string, len(raw.Args))
	}
	var operand comparisonOperand
	// Child errors precede arity, namespace, and signature diagnostics.
	for i, child := range raw.Args {
		bound, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		operand = bound
		types[i], err = b.extremaArgumentTypeName(bound)
		if err != nil {
			return nil, err
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, err := b.resolveExtremaSignature(raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	selected := arg.ResultUsage().Type
	usage := arg.ResultUsage()
	usage.Typmod = -1
	comparison, err := sqltype.BindComparison(selected, selected, usage.Collation, arg.CollationIndeterminate())
	if err != nil {
		return nil, err
	}
	operation := aggregateMin
	if raw.Name == "max" {
		operation = aggregateMax
	}
	provider := functions.NewExtremaProvider(usage, comparison, operation == aggregateMax)
	call := aggregateCall{provider: provider, argument: arg, operation: operation}
	call.routine, err = b.builtinRoutineReference(aggregateRoutineProvider, raw.Name, selected.ID())
	if err != nil {
		return nil, err
	}
	properties := arg.Properties()
	info := b.info(usage, raw.Pos, ExprProperties{StageDependent: true, ParameterDependent: properties.ParameterDependent, Volatility: properties.Volatility})
	info.CollationConflict, info.CollationExplicit = arg.CollationIndeterminate(), arg.ExplicitCollation()
	return b.finishAggregate(raw, call, filter, info)
}

func (b *expressionBinder) extremaArgumentTypeName(operand comparisonOperand) (string, error) {
	typ := operand.descriptor()
	if typ == nil {
		return "unknown", nil
	}
	if !typ.IsDomain() {
		return typ.DiagnosticName(), nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return "", err
	}
	path, err := b.engine.lookupPath(auth, typeLookup)
	if err != nil {
		return "", err
	}
	d := auth.catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	return typeDiagnosticNameAt(auth.catalog, d, path), nil
}

func (b *expressionBinder) resolveExtremaSignature(raw parser.AggregateExpr, name parser.ObjectName, operand comparisonOperand, types []string) (BoundExpr, error) {
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	selected := b.typ(sqltype.Text)
	if typ := operand.descriptor(); typ != nil {
		base := typ.BaseType()
		switch base.ID() {
		case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Oid, sqltype.Float4, sqltype.Float8, sqltype.Numeric,
			sqltype.Text, sqltype.BPChar, sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval, sqltype.Bytea:
			selected = base
		case sqltype.VarChar, sqltype.Name, sqltype.Char:
			selected = b.typ(sqltype.Text)
		case sqltype.Regclass:
			selected = b.typ(sqltype.Oid)
		case sqltype.NameArray, sqltype.PgNodeTree:
			return nil, expressionError("0A000", raw.Name+" for "+base.DiagnosticName()+" is unsupported", raw.Pos)
		default:
			return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
		}
	}
	return b.coerceConditional(operand, selected.Usage(), raw.Name)
}
