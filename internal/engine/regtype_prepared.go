package engine

import (
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type regtypeAnalysis struct {
	constants         []regtypeConstant
	path              sequence.Environment
	role              authorization.PrincipalRef
	database          string
	types, collations bool
	collationPath     []ids.ObjectAddress
	invalidated       bool // A failed reanalysis must not revive these captures.
}

func (e *Engine) regtypeAnalysisLocked(auth AuthorizationView, constants []regtypeConstant) (*regtypeAnalysis, error) {
	if len(constants) == 0 {
		return nil, nil
	}
	cell := &regtypeAnalysis{constants: slices.Clone(constants), role: auth.State.CurrentUser, database: auth.session.databaseName}
	for _, constant := range constants {
		if constant.Operator != 0 || constant.Value.Type() == nil || constant.Value.Type().ContainsRegtype() || constant.Value.Type().ContainsOperatorAlias() || constant.Value.Type().ContainsRegproc() {
			cell.types = true
		}
		for _, reference := range constant.References {
			cell.collations = cell.collations || reference.Address.ClassID == ids.CollationClassID
		}
	}
	var err error
	if cell.types {
		cell.path, err = e.sequenceEnvironmentLocked(auth)
	}
	if err == nil && cell.collations {
		cell.collationPath, err = e.lookupPath(auth, analyze.CollationLookup)
	}
	return cell, err
}

func (e *Engine) regtypeAnalysisValidLocked(auth AuthorizationView, cell *regtypeAnalysis) (bool, error) {
	if cell != nil && cell.invalidated {
		return false, nil
	}
	if cell == nil {
		return true, nil
	}
	if cell.types {
		path, err := e.sequenceEnvironmentLocked(auth)
		if err != nil {
			return false, err
		}
		if cell.role != auth.State.CurrentUser || cell.database != auth.session.databaseName || !sequence.SameEnvironment(cell.path, path) {
			return false, nil
		}
	}
	if cell.collations {
		path, err := e.lookupPath(auth, analyze.CollationLookup)
		if err != nil {
			return false, err
		}
		if cell.role != auth.State.CurrentUser || cell.database != auth.session.databaseName || !slices.Equal(cell.collationPath, path) {
			return false, nil
		}
	}
	// Preserve qualified namespace authority and operator operand stamps.
	// Collation/namespace aliases additionally reanalyze changed captured names.
	for _, constant := range cell.constants {
		for _, operand := range constant.Operands {
			d := auth.Catalog.Get(operand.Ref.Address)
			if d == nil || operand.Ref.Domain() != e.catalog.Domain || operand.Ref.Epoch != auth.Catalog.Epoch() || d.Revision() != operand.Revision {
				return false, nil
			}
		}
		for _, reference := range constant.References {
			d := auth.Catalog.Get(reference.Address)
			if d == nil || d.Name() != reference.Name || d.Namespace() != reference.Namespace {
				return false, nil
			}
			if reference.Address.ClassID == ids.CollationClassID {
				ns := auth.Catalog.Get(reference.Namespace)
				if ns == nil || ns.Name() != reference.NamespaceName {
					return false, nil
				}
				if err := auth.CanUse(e.catalog.Domain.Ref(auth.Catalog.Epoch(), reference.Namespace)); err != nil {
					if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
						return false, nil
					}
					return false, err
				}
			}
		}
		for _, namespace := range constant.Qualified {
			if auth.Catalog.Get(namespace) == nil {
				return false, nil
			}
			if err := auth.CanUse(e.catalog.Domain.Ref(auth.Catalog.Epoch(), namespace)); err != nil {
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
	constant := regtypeConstant{Offset: pos.Offset}
	found := false
	if b.expected != nil && b.expected.regtypes != nil {
		for _, c := range b.expected.regtypes.constants {
			if c.Offset == pos.Offset && c.Operator == 0 && c.TypeID == 0 {
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
		input, err := b.host.CaptureRegtypeInput(auth)
		if err != nil {
			return sqltype.Value{}, err
		}
		input.position = pos
		input.qualified = &constant.Qualified
		constant.ID, err = input.ResolveRegtype(b.ctx, text)
		if err != nil {
			return sqltype.Value{}, regtypeInputError(err, pos)
		}
	}
	b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
	return sqltype.RegtypeValue(b.typ(sqltype.Regtype), constant.ID)
}

func (b *expressionBinder) regtypeArrayConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if b.expected != nil && b.expected.regtypes != nil {
		for _, constant := range b.expected.regtypes.constants {
			if constant.Offset == pos.Offset && constant.Operator == 0 && constant.TypeID == 0 && constant.Value.Type() != nil {
				value, err := constant.Value.Retag(u.Type)
				if err != nil {
					return sqltype.Value{}, err
				}
				b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
				return value, nil
			}
		}
	}
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.input
	constant := regtypeConstant{Offset: pos.Offset}
	if resolver, ok := input.Regtype.(capturedRegtypeInput); ok {
		resolver.position = pos
		resolver.qualified = &constant.Qualified
		input.Regtype = resolver
	}
	value, err := b.decodeLiteralInput(text, u.Type, -1, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, regtypeInputError(err, pos)
	}
	constant.Value = value
	b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
	return value, nil
}

func (b *expressionBinder) foldRegtypeConstant(child expr.Expr, coercionPlan sqltype.CoercionPlan, info expr.Info) (expr.Expr, bool, error) {
	if (info.Usage.Type.BaseType().ID() != sqltype.Regtype && !sqltype.IsOperatorAlias(info.Usage.Type.BaseType().ID()) && !sqltype.IsRoutineAlias(info.Usage.Type.BaseType().ID()) && !sqltype.IsRegObjectAlias(info.Usage.Type.BaseType().ID()) && !sqltype.IsRegTS(info.Usage.Type.BaseType().ID())) || !(b.consumer.defaultConsumer || b.consumer.checkConsumer || b.consumer.indexConsumer || b.target.generatedOwner != nil || b.params.noParameters || b.consumer.materializedDefinition) {
		return nil, false, nil
	}
	p := child.Properties()
	id := child.ResultUsage().BaseUsage().Type.ID()
	if p.Volatility != expr.Immutable || p.RowDependent || p.ParameterDependent || p.StageDependent || p.DomainDependent || p.RuntimeDomainConversion || sqltype.ContainsDomainConstraints(info.Usage.Type) || id != sqltype.Oid && id != sqltype.Int2 && id != sqltype.Int4 && id != sqltype.Int8 && !sqltype.IsRoutineAlias(id) {
		return nil, false, nil
	}
	value, err := child.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: b.ctx, Input: b.input}})
	if err != nil {
		return nil, true, err
	}
	value, err = coercionPlan.Apply(b.ctx, value, b.input, sqltype.OutputEnvironment{})
	if err != nil {
		return nil, true, err
	}
	if !value.IsNull() {
		if value.Type().ContainsRegproc() {
			b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, regtypeConstant{Offset: info.Pos.Offset, TypeID: value.Type().ID(), Value: value})
			return plan.LiteralExpr{Info: info, Value: value}, true, nil
		}
		if value.Type().ContainsRegObject() {
			if err := b.recordRegObjectConstant(value, info.Pos); err != nil {
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
			constant := regtypeConstant{Offset: info.Pos.Offset, ID: id}
			if sqltype.IsOperatorAlias(value.Type().BaseType().ID()) || sqltype.IsRegTS(value.Type().BaseType().ID()) {
				constant.Operator = value.Type().BaseType().ID()
			}
			b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
		}
	}
	return plan.LiteralExpr{Info: info, Value: value}, true, nil
}

func (b *expressionBinder) recordRegObjectConstant(value sqltype.Value, pos parser.Position) error {
	constant := regtypeConstant{Offset: pos.Offset, Value: value}
	if err := b.captureRegObjectReferences(&constant); err != nil {
		return err
	}
	b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
	return nil
}

func (b *expressionBinder) captureRegObjectReferences(constant *regtypeConstant) error {
	view := b.bindingCatalog()
	return walkCatalogAliasDependencies(view, constant.Value, func(address ids.ObjectAddress) error {
		if address.ClassID == ids.TypeClassID {
			return nil
		}
		d := view.Get(address)
		reference := regObjectReference{Address: address, Name: d.Name(), Namespace: d.Namespace()}
		if address.ClassID == ids.CollationClassID {
			reference.NamespaceName = view.Get(d.Namespace()).Name()
		}
		constant.References = append(constant.References, reference)
		return nil
	})
}

func (b *expressionBinder) regObjectConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if b.expected != nil && b.expected.regtypes != nil {
		for _, constant := range b.expected.regtypes.constants {
			if constant.Offset == pos.Offset && constant.Operator == 0 && constant.Value.Type() != nil && constant.Value.Type().ID() == u.Type.ID() {
				value, err := constant.Value.Retag(u.Type)
				if err != nil {
					return sqltype.Value{}, err
				}
				for _, operand := range constant.Operands {
					b.dependency(operand.Ref, operand.Revision)
				}
				b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
				return value, nil
			}
		}
	}
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.input
	constant := regtypeConstant{Offset: pos.Offset}
	if u.Type.ContainsRegproc() {
		if resolver, ok := input.Regproc.(capturedRoutineInput); ok {
			resolver.types.position = pos
			resolver.types.qualified = &constant.Qualified
			input.Regproc = resolver
		}
		if resolver, ok := input.Regtype.(capturedRegtypeInput); ok {
			resolver.position = pos
			resolver.qualified = &constant.Qualified
			input.Regtype = resolver
		}
	}
	if u.Type.ContainsOperatorAlias() {
		if resolver, ok := input.Operator.(capturedOperatorInput); ok {
			resolver.types.position = pos
			resolver.qualified, resolver.operands = &constant.Qualified, &constant.Operands
			input.Operator = resolver
		}
	}
	value, err := u.Type.InputWithEnvironment(b.ctx, text, u.Typmod, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, regtypeInputError(err, pos)
	}
	constant.Value = value
	if err := b.captureRegObjectReferences(&constant); err != nil {
		return sqltype.Value{}, err
	}
	for _, operand := range constant.Operands {
		b.dependency(operand.Ref, operand.Revision)
	}
	b.prepared.RegtypeConstants = append(b.prepared.RegtypeConstants, constant)
	return value, nil
}
