package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// PLAssignment describes one invocation's semantic datum, not necessarily the
// whole lexical slot. A leading record field is published by the engine after
// the SELECT has accepted its cardinality.
type PLAssignment struct {
	Usage   sqltype.Usage
	Current sqltype.Value
	Steps   []parser.TargetStep
	Name    string
	Pos     parser.Position
	Raw     parser.Expr
}

// PLAssignment distinguishes absent assignment conversion from a selected
// catalog cast's error. Only the former permits the PL Output/Input rule.
func (b *Binder) PLAssignment(value sqltype.Value, target sqltype.Usage) (expr.Expr, bool, error) {
	node := plan.PLLocalExpr{Info: b.Info(value.Type().Usage(), parser.Position{}, expr.Properties{}), Value: value}
	return b.plAssignment(node, target)
}

func (b *Binder) plAssignment(node expr.Expr, target sqltype.Usage) (expr.Expr, bool, error) {
	if err := b.CaptureTypeDiagnosticInput(target.Type); err != nil {
		return nil, false, err
	}
	if selected, handled, err := b.declaredCoercion(node, target, sqltype.Assignment, parser.Position{}, nil); handled {
		return selected, true, err
	}
	conversion, err := sqltype.PlanCoercion(node.ResultUsage(), target, sqltype.Assignment)
	if err != nil {
		if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" {
			return nil, false, nil
		}
		return nil, false, err
	}
	return plan.CoercionExpr{Info: b.Info(target, parser.Position{}, CoercionProperties(node, conversion)), Child: node, Plan: conversion, Mode: sqltype.Assignment}, true, nil
}

// PLAssignmentExpression retains the bound child's dependence and coercion
// selection. IO is selected only for an absent assignment path, never retried
// after executing a selected cast.
func (b *Binder) PLAssignmentExpression(child expr.Expr, target sqltype.Usage) (expr.Expr, error) {
	node, err := b.plAssignmentConversion(child, target)
	if err != nil {
		return nil, err
	}
	return plan.PLAssignmentExpr{Info: expr.InfoOf(node), Child: node}, nil
}

func (b *Binder) plAssignmentConversion(child expr.Expr, target sqltype.Usage) (expr.Expr, error) {
	node, selected, err := b.plAssignment(child, target)
	if err != nil {
		return nil, err
	}
	if !selected {
		base := target.BaseUsage()
		info, err := b.coercionInfo(b.Info(base, child.SourcePosition(), child.Properties()), child)
		if err != nil {
			return nil, err
		}
		conversion, err := plan.NewDatumCast(info, child, "i", sqltype.Assignment)
		if err != nil {
			return nil, err
		}
		node = plan.PLIOCoercionExpr{DatumCastExpr: conversion}
		if base != target {
			node, err = b.CoerceSelected(node, target, sqltype.Assignment, child.SourcePosition(), nil)
			if err != nil {
				return nil, err
			}
		}
	}
	return node, nil
}

func (b *Binder) bindPLAssignmentProjection(child expr.Expr) (expr.Expr, error) {
	return b.PLAssignmentTargetExpression(child, b.plAssignmentDatum)
}

// PLAssignmentTargetExpression binds one reached semantic datum without publishing
// activation state. Query projections and detached FETCH values share this owner.
func (b *Binder) PLAssignmentTargetExpression(child expr.Expr, assignment *PLAssignment) (expr.Expr, error) {
	if len(assignment.Steps) == 0 {
		return b.PLAssignmentExpression(child, assignment.Usage)
	}
	if err := b.CaptureTypeDiagnosticInput(assignment.Usage.Type); err != nil {
		return nil, err
	}
	target, err := b.BindAssignmentTarget(catalog.Column{Name: assignment.Name, Usage: assignment.Usage}, assignment.Steps, assignment.Pos)
	if err != nil {
		return nil, err
	}
	child, err = b.plAssignmentConversion(child, target.Destination)
	if err != nil {
		return nil, err
	}
	current := plan.PLLocalExpr{Info: b.Info(assignment.Usage, assignment.Pos, expr.Properties{ParameterDependent: true}), Value: assignment.Current}
	properties := expr.MergeProperties(current.Properties(), child.Properties())
	for node := target; node != nil; node = node.Next {
		node.DomainErrorOrigin = true
		for _, bound := range node.Node.Subscripts {
			for _, expression := range [2]expr.Expr{bound.Upper, bound.Lower} {
				if expression != nil {
					properties = expr.MergeProperties(properties, expression.Properties())
				}
			}
		}
	}
	node := plan.PLAssignmentTargetExpr{Info: b.Info(assignment.Usage, assignment.Pos, properties), Current: current, Child: child, Target: target, SetProjection: b.Clause.QueryHasTargetSet}
	return plan.PLAssignmentExpr{Info: expr.InfoOf(node), Child: node}, nil
}
