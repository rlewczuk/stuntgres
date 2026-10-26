package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) RegtypeConstant(text string, pos parser.Position) (sqltype.Value, error) {
	constant := RegtypeConstant{Offset: pos.Offset}
	found := false
	for _, c := range b.Expected.Regtypes {
		if c.Offset == pos.Offset && c.Operator == 0 && c.TypeID == 0 {
			constant, found = c, true
			break
		}
	}
	if !found {
		auth, err := b.BindingAuthorization()
		if err != nil {
			return sqltype.Value{}, err
		}
		constant.ID, constant.Qualified, err = b.Host.ResolveRegtypeText(b.Ctx, auth, pos, text)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, constant)
	return sqltype.RegtypeValue(b.Type(sqltype.Regtype), constant.ID)
}

func (b *Binder) regtypeArrayConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	for _, constant := range b.Expected.Regtypes {
		if constant.Offset == pos.Offset && constant.Operator == 0 && constant.TypeID == 0 && constant.Value.Type() != nil {
			value, err := constant.Value.Retag(u.Type)
			if err != nil {
				return sqltype.Value{}, err
			}
			b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, constant)
			return value, nil
		}
	}
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.Input
	constant := RegtypeConstant{Offset: pos.Offset}
	if resolver, ok := input.Regtype.(RegtypeRetarget); ok {
		input.Regtype = resolver.RetargetRegtype(IdentifierTarget{Position: pos, Qualified: &constant.Qualified})
	}
	value, err := b.decodeLiteralInput(text, u.Type, -1, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, RegtypeInputError(err, pos)
	}
	constant.Value = value
	b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, constant)
	return value, nil
}

func (b *Binder) foldRegtypeConstant(child expr.Expr, coercionPlan sqltype.CoercionPlan, info expr.Info) (expr.Expr, bool, error) {
	if (info.Usage.Type.BaseType().ID() != sqltype.Regtype && !sqltype.IsOperatorAlias(info.Usage.Type.BaseType().ID()) && !sqltype.IsRoutineAlias(info.Usage.Type.BaseType().ID()) && !sqltype.IsRegObjectAlias(info.Usage.Type.BaseType().ID()) && !sqltype.IsRegTS(info.Usage.Type.BaseType().ID())) || !(b.Consumer.DefaultConsumer || b.Consumer.CheckConsumer || b.Consumer.IndexConsumer || b.Target.GeneratedOwner != nil || b.Params.NoParameters || b.Consumer.frozenDefinition) {
		return nil, false, nil
	}
	p := child.Properties()
	id := child.ResultUsage().BaseUsage().Type.ID()
	if p.Volatility != expr.Immutable || p.RowDependent || p.ParameterDependent || p.StageDependent || p.DomainDependent || p.RuntimeDomainConversion || sqltype.ContainsDomainConstraints(info.Usage.Type) || id != sqltype.Oid && id != sqltype.Int2 && id != sqltype.Int4 && id != sqltype.Int8 && !sqltype.IsRoutineAlias(id) {
		return nil, false, nil
	}
	value, err := child.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: b.Ctx, Input: b.Input}})
	if err != nil {
		return nil, true, err
	}
	value, err = coercionPlan.Apply(b.Ctx, value, b.Input, sqltype.OutputEnvironment{})
	if err != nil {
		return nil, true, err
	}
	if !value.IsNull() {
		if value.Type().ContainsRegproc() {
			b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, RegtypeConstant{Offset: info.Pos.Offset, TypeID: value.Type().ID(), Value: value})
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
			constant := RegtypeConstant{Offset: info.Pos.Offset, ID: id}
			if sqltype.IsOperatorAlias(value.Type().BaseType().ID()) || sqltype.IsRegTS(value.Type().BaseType().ID()) {
				constant.Operator = value.Type().BaseType().ID()
			}
			b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, constant)
		}
	}
	return plan.LiteralExpr{Info: info, Value: value}, true, nil
}

func (b *Binder) recordRegObjectConstant(value sqltype.Value, pos parser.Position) error {
	constant := RegtypeConstant{Offset: pos.Offset, Value: value}
	if err := b.captureRegObjectReferences(&constant); err != nil {
		return err
	}
	b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, constant)
	return nil
}

func (b *Binder) captureRegObjectReferences(constant *RegtypeConstant) error {
	view := b.BindingCatalog()
	return catalog.WalkAliasDependencies(view, constant.Value, func(address ids.ObjectAddress) error {
		if address.ClassID == ids.TypeClassID {
			return nil
		}
		d := view.Get(address)
		reference := RegObjectReference{Address: address, Name: d.Name(), Namespace: d.Namespace()}
		if address.ClassID == ids.CollationClassID {
			reference.NamespaceName = view.Get(d.Namespace()).Name()
		}
		constant.References = append(constant.References, reference)
		return nil
	})
}

func (b *Binder) regObjectConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	for _, constant := range b.Expected.Regtypes {
		if constant.Offset == pos.Offset && constant.Operator == 0 && constant.Value.Type() != nil && constant.Value.Type().ID() == u.Type.ID() {
			value, err := constant.Value.Retag(u.Type)
			if err != nil {
				return sqltype.Value{}, err
			}
			for _, operand := range constant.Operands {
				b.captureDependency(operand.Ref, operand.Revision, catalog.OperandDependency)
			}
			b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, constant)
			return value, nil
		}
	}
	if err := b.captureElementInput(); err != nil {
		return sqltype.Value{}, err
	}
	input := b.Input
	constant := RegtypeConstant{Offset: pos.Offset}
	if u.Type.ContainsRegproc() {
		// Routine names record no operands here.
		target := IdentifierTarget{Position: pos, Qualified: &constant.Qualified}
		if resolver, ok := input.Regproc.(RoutineRetarget); ok {
			input.Regproc = resolver.RetargetRoutine(target)
		}
		if resolver, ok := input.Regtype.(RegtypeRetarget); ok {
			input.Regtype = resolver.RetargetRegtype(target)
		}
	}
	if u.Type.ContainsOperatorAlias() {
		if resolver, ok := input.Operator.(OperatorRetarget); ok {
			input.Operator = resolver.RetargetOperator(IdentifierTarget{Position: pos, Qualified: &constant.Qualified, Operands: &constant.Operands})
		}
	}
	value, err := u.Type.InputWithEnvironment(b.Ctx, text, u.Typmod, input.LiteralInput(pos.Character))
	if err != nil {
		return sqltype.Value{}, RegtypeInputError(err, pos)
	}
	constant.Value = value
	if err := b.captureRegObjectReferences(&constant); err != nil {
		return sqltype.Value{}, err
	}
	for _, operand := range constant.Operands {
		b.captureDependency(operand.Ref, operand.Revision, catalog.OperandDependency)
	}
	b.Prepared.RegtypeConstants = append(b.Prepared.RegtypeConstants, constant)
	return value, nil
}
