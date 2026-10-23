package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// PLAssignment distinguishes absent assignment conversion from a selected
// catalog cast's error. Only the former permits the PL Output/Input rule.
func (b *Binder) PLAssignment(value sqltype.Value, target sqltype.Usage) (expr.Expr, bool, error) {
	if err := b.CaptureTypeDiagnosticInput(target.Type); err != nil {
		return nil, false, err
	}
	node := plan.PLLocalExpr{Info: b.Info(value.Type().Usage(), parser.Position{}, expr.Properties{}), Value: value}
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
