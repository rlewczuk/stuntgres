package engine

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type regtypeConstant struct {
	offset     int
	operator   sqltype.ID
	typeID     sqltype.ID
	id         uint32
	value      sqltype.Value
	qualified  []ObjectAddress
	operands   []expressionDependency
	references []regObjectReference
}
type regtypeAnalysis struct {
	constants         []regtypeConstant
	path              sequenceEnvironment
	role              authorization.PrincipalRef
	database          string
	types, collations bool
	collationPath     []ObjectAddress
}

type regObjectReference struct {
	address       ObjectAddress
	name          string
	namespace     ObjectAddress
	namespaceName string
}

func (e *Engine) regtypeAnalysisLocked(auth AuthorizationView, constants []regtypeConstant) (*regtypeAnalysis, error) {
	if len(constants) == 0 {
		return nil, nil
	}
	cell := &regtypeAnalysis{constants: slices.Clone(constants), role: auth.state.CurrentUser, database: auth.session.databaseName}
	for _, constant := range constants {
		if constant.operator != 0 || constant.value.Type() == nil || constant.value.Type().ContainsRegtype() || constant.value.Type().ContainsOperatorAlias() || constant.value.Type().ContainsRegproc() {
			cell.types = true
		}
		for _, reference := range constant.references {
			cell.collations = cell.collations || reference.address.ClassID == CollationClassID
		}
	}
	var err error
	if cell.types {
		cell.path, err = e.sequenceEnvironmentLocked(auth)
	}
	if err == nil && cell.collations {
		cell.collationPath, err = e.lookupPath(auth, collationLookup)
	}
	return cell, err
}

func (e *Engine) regtypeAnalysisValidLocked(auth AuthorizationView, cell *regtypeAnalysis) (bool, error) {
	if cell == nil {
		return true, nil
	}
	if cell.types {
		path, err := e.sequenceEnvironmentLocked(auth)
		if err != nil {
			return false, err
		}
		if cell.role != auth.state.CurrentUser || cell.database != auth.session.databaseName || !sameSequenceEnvironment(cell.path, path) {
			return false, nil
		}
	}
	if cell.collations {
		path, err := e.lookupPath(auth, collationLookup)
		if err != nil {
			return false, err
		}
		if cell.role != auth.state.CurrentUser || cell.database != auth.session.databaseName || !slices.Equal(cell.collationPath, path) {
			return false, nil
		}
	}
	// Preserve qualified namespace authority and operator operand stamps.
	// Collation/namespace aliases additionally reanalyze changed captured names.
	for _, constant := range cell.constants {
		for _, operand := range constant.operands {
			d := auth.catalog.state.descriptors[operand.ref.Address]
			if d == nil || operand.ref.owner != e.owner || operand.ref.Epoch != auth.catalog.Epoch() || d.Revision() != operand.revision {
				return false, nil
			}
		}
		for _, reference := range constant.references {
			d := auth.catalog.state.descriptors[reference.address]
			if d == nil || d.Name() != reference.name || d.Namespace() != reference.namespace {
				return false, nil
			}
			if reference.address.ClassID == CollationClassID {
				ns := auth.catalog.state.descriptors[reference.namespace]
				if ns == nil || ns.Name() != reference.namespaceName {
					return false, nil
				}
				if err := auth.CanUse(ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: reference.namespace}); err != nil {
					if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
						return false, nil
					}
					return false, err
				}
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
	if (info.usage.Type.BaseType().ID() != sqltype.Regtype && !sqltype.IsOperatorAlias(info.usage.Type.BaseType().ID()) && !sqltype.IsRoutineAlias(info.usage.Type.BaseType().ID()) && !sqltype.IsRegObjectAlias(info.usage.Type.BaseType().ID()) && !sqltype.IsRegTS(info.usage.Type.BaseType().ID())) || !(b.defaultConsumer || b.checkConsumer || b.indexConsumer || b.generatedOwner != nil || b.noParameters || b.materializedDefinition) {
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
		if value.Type().ContainsRegObject() {
			if err := b.recordRegObjectConstant(value, info.pos); err != nil {
				return nil, true, err
			}
		} else {
			var id uint32
			var err error
			if sqltype.IsOperatorAlias(value.Type().BaseType().ID()) {
				id, err = value.OperatorOID()
			} else if sqltype.IsRegTS(value.Type().BaseType().ID()) {
				id, err = value.RegTSOID()
			} else {
				id, err = value.TypeOID()
			}
			if err != nil {
				return nil, true, err
			}
			constant := regtypeConstant{offset: info.pos.Offset, id: id}
			if sqltype.IsOperatorAlias(value.Type().BaseType().ID()) || sqltype.IsRegTS(value.Type().BaseType().ID()) {
				constant.operator = value.Type().BaseType().ID()
			}
			b.regtypeConstants = append(b.regtypeConstants, constant)
		}
	}
	return literalExpression{info, value}, true, nil
}

func (b *expressionBinder) recordRegObjectConstant(value sqltype.Value, pos parser.Position) error {
	constant := regtypeConstant{offset: pos.Offset, value: value}
	if err := b.captureRegObjectReferences(&constant); err != nil {
		return err
	}
	b.regtypeConstants = append(b.regtypeConstants, constant)
	return nil
}

func (b *expressionBinder) captureRegObjectReferences(constant *regtypeConstant) error {
	view := b.bindingCatalog()
	return walkCatalogAliasDependencies(view, constant.value, func(address ObjectAddress) error {
		if address.ClassID == TypeClassID {
			return nil
		}
		d := view.state.descriptors[address]
		reference := regObjectReference{address: address, name: d.Name(), namespace: d.Namespace()}
		if address.ClassID == CollationClassID {
			reference.namespaceName = view.state.descriptors[d.Namespace()].Name()
		}
		constant.references = append(constant.references, reference)
		return nil
	})
}

func (b *expressionBinder) regObjectConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if b.expected != nil && b.expected.regtypes != nil {
		for _, constant := range b.expected.regtypes.constants {
			if constant.offset == pos.Offset && constant.operator == 0 && constant.value.Type() != nil && constant.value.Type().ID() == u.Type.ID() {
				value, err := constant.value.Retag(u.Type)
				if err != nil {
					return sqltype.Value{}, err
				}
				for _, operand := range constant.operands {
					b.dependency(operand.ref, operand.revision)
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
	if u.Type.ContainsRegproc() {
		if resolver, ok := input.Regproc.(capturedRoutineInput); ok {
			resolver.types.position = pos
			resolver.types.qualified = &constant.qualified
			input.Regproc = resolver
		}
		if resolver, ok := input.Regtype.(capturedRegtypeInput); ok {
			resolver.position = pos
			resolver.qualified = &constant.qualified
			input.Regtype = resolver
		}
	}
	if u.Type.ContainsOperatorAlias() {
		if resolver, ok := input.Operator.(capturedOperatorInput); ok {
			resolver.types.position = pos
			resolver.qualified, resolver.operands = &constant.qualified, &constant.operands
			input.Operator = resolver
		}
	}
	value, err := u.Type.InputWithEnvironment(b.ctx, text, u.Typmod, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, regtypeInputError(err, pos)
	}
	constant.value = value
	if err := b.captureRegObjectReferences(&constant); err != nil {
		return sqltype.Value{}, err
	}
	for _, operand := range constant.operands {
		b.dependency(operand.ref, operand.revision)
	}
	b.regtypeConstants = append(b.regtypeConstants, constant)
	return value, nil
}
