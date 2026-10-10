package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type stringAggregateProvider struct{ usage sqltype.Usage }
type stringAggregateState struct {
	typ    *sqltype.Descriptor
	seen   bool
	output strings.Builder
}

func (p stringAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p stringAggregateProvider) valid() bool {
	return p.usage.Type != nil && (p.usage.Type.ID() == sqltype.Text || p.usage.Type.ID() == sqltype.Bytea)
}
func (p stringAggregateProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.valid() {
		return nil, fmt.Errorf("stuntgres: invalid string aggregate descriptor")
	}
	return &stringAggregateState{typ: p.usage.Type}, nil
}
func (p stringAggregateProvider) Advance(ctx context.Context, state any, value, separator sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*stringAggregateState)
	if !ok || s == nil || !p.valid() || s.typ != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid string aggregate state")
	}
	if value.Type() != p.usage.Type || separator.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid string aggregate datum")
	}
	if value.IsNull() {
		return nil
	}
	payload := func(v sqltype.Value) (string, error) {
		if p.usage.Type.ID() == sqltype.Bytea {
			return v.Binary()
		}
		return v.Text()
	}
	v, err := payload(value)
	if err != nil {
		return err
	}
	var d string
	if s.seen && !separator.IsNull() {
		d, err = payload(separator)
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.output.WriteString(d)
	s.output.WriteString(v)
	s.seen = true
	return ctx.Err()
}
func (p stringAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*stringAggregateState)
	if !ok || s == nil || !p.valid() || s.typ != p.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid string aggregate state")
	}
	if !s.seen {
		return sqltype.Null(p.usage.Type), nil
	}
	if p.usage.Type.ID() == sqltype.Bytea {
		return sqltype.Binary(p.usage.Type, s.output.String())
	}
	return sqltype.String(p.usage.Type, s.output.String()), nil
}

func (b *expressionBinder) bindStringAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	var small [2]comparisonOperand
	var names [2]string
	var operands []comparisonOperand
	var types []string
	if len(raw.Args) <= 2 {
		operands = small[:len(raw.Args)]
		types = names[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
			types[i] = "unknown"
			if typ := o.descriptor(); typ != nil {
				types[i] = typ.DiagnosticName()
			}
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	value, separator, err := b.resolveStringAggregateSignature(raw, name, operands, types)
	if err != nil {
		return nil, err
	}
	properties := mergeExpressionProperties(value.Properties(), separator.Properties())
	properties.StageDependent = true
	info, err := collatedInfo(b.info(value.ResultUsage().Type.Usage(), raw.Pos, properties), value, separator)
	if err != nil {
		return nil, err
	}
	call := aggregateCall{provider: stringAggregateProvider{info.usage}, argument: value, separator: separator, operation: aggregateString}
	return b.finishAggregate(raw, call, filter, info)
}

func (b *expressionBinder) resolveStringAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, operands []comparisonOperand, types []string) (BoundExpr, BoundExpr, error) {
	if len(raw.Args) > 100 {
		return nil, nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, nil, err
	}
	if !visible || raw.Star || len(operands) != 2 {
		return nil, nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	typ := b.typ(sqltype.Text)
	for _, o := range operands {
		if d := o.descriptor(); d != nil && d.BaseType().ID() == sqltype.Bytea {
			typ = b.typ(sqltype.Bytea)
		}
	}
	for _, o := range operands {
		if d := o.descriptor(); d != nil {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), typ.Usage(), sqltype.Implicit); err != nil {
				return nil, nil, functionSignatureErrorForTypes(name, raw.Pos, types)
			}
		}
	}
	value, err := b.coerceConditional(operands[0], typ.Usage(), raw.Name)
	if err != nil {
		return nil, nil, err
	}
	separator, err := b.coerceConditional(operands[1], typ.Usage(), raw.Name)
	return value, separator, err
}
