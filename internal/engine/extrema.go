package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A provider is immutable; each execution/group owns just one retained datum.
// Comparison is demanded only after the first eligible candidate.
type extremaProvider struct {
	usage             sqltype.Usage
	comparison        sqltype.Comparison
	maximum           bool
	collationConflict bool
}
type extremaState struct{ value sqltype.Value }

func (p extremaProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p extremaProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.usage.Type.Concrete() || p.comparison.SessionDependent() {
		return nil, fmt.Errorf("stuntgres: invalid extrema result type")
	}
	return &extremaState{value: sqltype.Null(p.usage.Type)}, nil
}
func (p extremaProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*extremaState)
	if !ok || s == nil || !p.usage.Type.Concrete() || s.value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid extrema state")
	}
	if value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid extrema datum")
	}
	if value.IsNull() {
		return nil
	}
	if s.value.IsNull() {
		s.value = value
		return ctx.Err()
	}
	if p.collationConflict {
		return conditionalCollationError()
	}
	sign, err := p.comparison.Compare(ctx, value, s.value, sqltype.InputEnvironment{})
	if err != nil {
		return err
	}
	// BPCHAR's release transition retains the prior equal representation. Other
	// strict reducers choose the incoming datum (scale, interval fields, ±0).
	if sign == 0 && p.usage.Type.ID() != sqltype.BPChar || p.maximum && sign > 0 || !p.maximum && sign < 0 {
		s.value = value
	}
	return ctx.Err()
}
func (p extremaProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*extremaState)
	if !ok || s == nil || !p.usage.Type.Concrete() || s.value.Type() != p.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid extrema state")
	}
	return s.value, nil
}

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
		types[i] = "unknown"
		if typ := bound.descriptor(); typ != nil {
			types[i] = typ.DiagnosticName()
			if typ.IsDomain() {
				auth, err := b.bindingAuthorization()
				if err != nil {
					return nil, err
				}
				path, err := b.engine.lookupPath(auth, typeLookup)
				if err != nil {
					return nil, err
				}
				d := auth.catalog.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(typ.ID())}].(*typeDescriptor)
				types[i] = typeDiagnosticNameAt(auth.catalog, d, path)
			}
		}
	}
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
	var arg BoundExpr
	if typ := operand.descriptor(); typ != nil && typ.BaseType().ID() == sqltype.Char {
		info := b.info(selected.Usage(), operand.node.SourcePosition(), operand.node.Properties())
		plan, failure := sqltype.PlanExtremaCharText(operand.node.ResultUsage(), info.usage)
		if failure != nil {
			return nil, failure
		}
		arg = coercionExpression{info, operand.node, plan, sqltype.Implicit}
	} else {
		arg, err = b.coerceConditional(operand, selected.Usage(), raw.Name)
		if err != nil {
			return nil, err
		}
	}
	if inner := aggregateOccurrence(arg); inner != nil {
		return nil, expressionError("42803", "aggregate function calls cannot be nested", inner.pos)
	}
	usage := arg.ResultUsage()
	usage.Typmod = -1
	comparison, err := sqltype.BindComparison(selected, selected)
	if err != nil {
		return nil, err
	}
	operation := aggregateMin
	if raw.Name == "max" {
		operation = aggregateMax
	}
	provider := extremaProvider{usage: usage, comparison: comparison, maximum: operation == aggregateMax, collationConflict: arg.CollationIndeterminate()}
	call := aggregateCall{provider: provider, argument: arg, operation: operation}
	slot := len(b.aggregates)
	if arg.Properties().Volatility == Immutable {
		for i, existing := range b.aggregates {
			if existing.operation == operation && existing.provider.ResultUsage() == usage && b.sameGroupExpression(arg, existing.argument) {
				slot = i
				break
			}
		}
	}
	if slot == len(b.aggregates) {
		b.aggregates = append(b.aggregates, call)
	}
	properties := arg.Properties()
	info := b.info(usage, raw.Pos, ExprProperties{StageDependent: true, ParameterDependent: properties.ParameterDependent, Volatility: properties.Volatility})
	info.collationConflict, info.collationExplicit = arg.CollationIndeterminate(), arg.ExplicitCollation()
	return aggregateExpression{info, slot}, nil
}
