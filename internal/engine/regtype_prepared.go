package engine

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type regtypeConstant struct {
	offset    int
	operator  sqltype.ID
	typeID    sqltype.ID
	id        uint32
	value     sqltype.Value
	qualified []ObjectAddress
	operands  []expressionDependency
}
type regtypeAnalysis struct {
	constants []regtypeConstant
	path      sequenceEnvironment
	role      authorization.PrincipalRef
	database  string
}

func (e *Engine) regtypeAnalysisLocked(auth AuthorizationView, constants []regtypeConstant) (*regtypeAnalysis, error) {
	if len(constants) == 0 {
		return nil, nil
	}
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return nil, err
	}
	return &regtypeAnalysis{constants: slices.Clone(constants), path: path, role: auth.state.CurrentUser, database: auth.session.databaseName}, nil
}

func (e *Engine) regtypeAnalysisValidLocked(auth AuthorizationView, cell *regtypeAnalysis) (bool, error) {
	if cell == nil {
		return true, nil
	}
	path, err := e.sequenceEnvironmentLocked(auth)
	if err != nil {
		return false, err
	}
	if cell.role != auth.state.CurrentUser || cell.database != auth.session.databaseName || !sameSequenceEnvironment(cell.path, path) {
		return false, nil
	}
	// Qualified namespaces add authority beyond the path. Named operator
	// operands also retain definition stamps; ordinary REGTYPE referent drops
	// and unrelated role DDL keep their existing behavior.
	for _, constant := range cell.constants {
		for _, operand := range constant.operands {
			d := auth.catalog.state.descriptors[operand.ref.Address]
			if d == nil || operand.ref.owner != e.owner || operand.ref.Epoch != auth.catalog.Epoch() || d.Revision() != operand.revision {
				return false, nil
			}
		}
		for _, namespace := range constant.qualified {
			if auth.catalog.state.descriptors[namespace] == nil {
				return false, nil
			}
			if err := auth.CanUse(ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: namespace}); err != nil {
				if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
					return false, nil
				}
				return false, err
			}
		}
	}
	return true, nil
}

func (b *expressionBinder) regtypeConstant(text string, pos parser.Position) (sqltype.Value, error) {
	constant := regtypeConstant{offset: pos.Offset}
	found := false
	if b.expected != nil && b.expected.regtypes != nil {
		for _, c := range b.expected.regtypes.constants {
			if c.offset == pos.Offset && c.operator == 0 && c.typeID == 0 {
				constant, found = c, true
				break
			}
		}
	}
	if !found {
		auth, err := b.bindingAuthorization()
		if err != nil {
			return sqltype.Value{}, err
		}
		input, err := b.engine.captureRegtypeInput(auth, b.op)
		if err != nil {
			return sqltype.Value{}, err
		}
		input.position = pos
		input.qualified = &constant.qualified
		constant.id, err = input.ResolveRegtype(b.ctx, text)
		if err != nil {
			return sqltype.Value{}, regtypeInputError(err, pos)
		}
	}
	b.regtypeConstants = append(b.regtypeConstants, constant)
	return sqltype.RegtypeValue(b.typ(sqltype.Regtype), constant.id)
}

func (b *expressionBinder) regtypeArrayConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if b.expected != nil && b.expected.regtypes != nil {
		for _, constant := range b.expected.regtypes.constants {
			if constant.offset == pos.Offset && constant.operator == 0 && constant.typeID == 0 && constant.value.Type() != nil {
				value, err := constant.value.Retag(u.Type)
				if err != nil {
					return sqltype.Value{}, err
				}
				b.regtypeConstants = append(b.regtypeConstants, constant)
				return value, nil
			}
		}
	}
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.input
	constant := regtypeConstant{offset: pos.Offset}
	if resolver, ok := input.Regtype.(capturedRegtypeInput); ok {
		resolver.position = pos
		resolver.qualified = &constant.qualified
		input.Regtype = resolver
	}
	value, err := u.Type.InputWithEnvironment(b.ctx, text, -1, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, regtypeInputError(err, pos)
	}
	constant.value = value
	b.regtypeConstants = append(b.regtypeConstants, constant)
	return value, nil
}

func (b *expressionBinder) foldRegtypeConstant(child BoundExpr, plan sqltype.CoercionPlan, info expressionInfo) (BoundExpr, bool, error) {
	if (info.usage.Type.BaseType().ID() != sqltype.Regtype && !sqltype.IsOperatorAlias(info.usage.Type.BaseType().ID()) && !sqltype.IsRoutineAlias(info.usage.Type.BaseType().ID())) || !(b.defaultConsumer || b.checkConsumer || b.indexConsumer || b.generatedOwner != nil || b.noParameters || b.materializedDefinition) {
		return nil, false, nil
	}
	p := child.Properties()
	id := child.ResultUsage().BaseUsage().Type.ID()
	if p.Volatility != Immutable || p.RowDependent || p.ParameterDependent || p.StageDependent || id != sqltype.Oid && id != sqltype.Int2 && id != sqltype.Int4 && id != sqltype.Int8 && !sqltype.IsRoutineAlias(id) {
		return nil, false, nil
	}
	value, err := child.Eval(&EvalContext{Context: b.ctx, Input: b.input})
	if err != nil {
		return nil, true, err
	}
	value, err = plan.Apply(b.ctx, value, b.input, sqltype.OutputEnvironment{})
	if err != nil {
		return nil, true, err
	}
	if !value.IsNull() {
		if value.Type().ContainsRegproc() {
			b.regtypeConstants = append(b.regtypeConstants, regtypeConstant{offset: info.pos.Offset, typeID: value.Type().ID(), value: value})
			return literalExpression{info, value}, true, nil
		}
		var id uint32
		var err error
		if sqltype.IsOperatorAlias(value.Type().BaseType().ID()) {
			id, err = value.OperatorOID()
		} else {
			id, err = value.TypeOID()
		}
		if err != nil {
			return nil, true, err
		}
		constant := regtypeConstant{offset: info.pos.Offset, id: id}
		if sqltype.IsOperatorAlias(value.Type().BaseType().ID()) {
			constant.operator = value.Type().BaseType().ID()
		}
		b.regtypeConstants = append(b.regtypeConstants, constant)
	}
	return literalExpression{info, value}, true, nil
}
