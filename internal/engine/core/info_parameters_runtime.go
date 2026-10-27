package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// RoutineDefault presents a demanded declaration expression. The producer owns
// disclosure; this capability neither authorizes, evaluates, nor serializes replay.
func (r EngineCatalogRuntime) RoutineDefault(ctx context.Context, capture catalog.Read, d *catalog.Procedure, slot int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if capture != r.Auth.Catalog {
		return "", ids.ErrCatalogReplaced
	}
	if err := validateRoutineDefaultOwner(capture, d); err != nil {
		return "", err
	}
	node, err := r.routineDefaultNode(ctx, capture, d, slot)
	if err != nil {
		return "", err
	}
	renderer := stored.Deparser{View: capture, Path: r.presentation.Path, Output: r.presentation.Output, NonstandardStrings: r.presentation.NonstandardStrings, RoutineDeclaration: true}
	return renderer.SQL(ctx, node, stored.DeparseParent{})
}

// validateRoutineDefaultOwner checks the shared owner once per acquisition, not
// once per element of a potentially large SQL declaration-default list.
func validateRoutineDefaultOwner(capture catalog.Read, d *catalog.Procedure) error {
	if d == nil || capture.Get(d.Address()) != d {
		return ids.ErrCatalogReplaced
	}
	if d.SQL == nil {
		if d.SQLDefaults != nil {
			return fmt.Errorf("stuntgres: invalid routine literal default authority")
		}
		return nil
	}
	if d.LiteralDefaults != nil || d.SQLDefaults == nil {
		return fmt.Errorf("stuntgres: missing checked routine defaults")
	}
	if err := d.SQLDefaults.ValidateDefinition(d); err != nil {
		return err
	}
	return d.SQLDefaults.ValidateMetadata(capture)
}

// routineDefaultNode reads a validated declaration owner, never evaluating a
// default or invoking a routine. Literal recipes use only type input/coercion.
func (r EngineCatalogRuntime) routineDefaultNode(ctx context.Context, capture catalog.Read, d *catalog.Procedure, slot int) (expr.Expr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.SQL != nil {
		node, ok := d.SQLDefaults.Default(slot)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing routine declaration default")
		}
		return node, nil
	}
	count := int(d.Installed.Defaults)
	if d.Builtin != nil {
		count = int(d.Builtin.Defaults)
	}
	inputs := 0
	remaining := string(d.Signature)
	var destination ids.OID
	for remaining != "" {
		token, rest, _ := strings.Cut(remaining, ",")
		remaining = rest
		id, err := strconv.ParseUint(token, 10, 32)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("stuntgres: invalid default input signature")
		}
		if inputs == slot {
			destination = ids.OID(id)
		}
		inputs++
	}
	if count == 0 || count > inputs || d.LiteralDefaults.Len() != count || slot < inputs-count || slot >= inputs {
		return nil, fmt.Errorf("stuntgres: invalid routine literal default layout")
	}
	recipe := d.LiteralDefaults.At(slot - (inputs - count))
	switch sqltype.ID(recipe.SourceType) {
	case sqltype.Bool, sqltype.Int4, sqltype.Int8, sqltype.Float8, sqltype.Numeric, sqltype.Text, sqltype.JSONB:
	default:
		return nil, fmt.Errorf("stuntgres: invalid routine default literal type")
	}
	source, ok := capture.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: recipe.SourceType})
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing routine default literal type")
	}
	target, ok := capture.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: destination})
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing routine default input type")
	}
	if err := r.ValidateType(capture, source); err != nil {
		return nil, err
	}
	if err := r.ValidateType(capture, target); err != nil {
		return nil, err
	}
	value := sqltype.Null(source.SQLType)
	if recipe.Null {
		if recipe.Text != "" {
			return nil, fmt.Errorf("stuntgres: malformed routine NULL default")
		}
	} else {
		var err error
		value, err = source.SQLType.Input(ctx, recipe.Text, -1)
		if err != nil {
			return nil, err
		}
	}
	var node expr.Expr = plan.LiteralExpr{Info: expr.Info{Usage: source.SQLType.Usage()}, Value: value}
	coercion, err := sqltype.PlanCoercion(source.SQLType.Usage(), target.SQLType.Usage(), sqltype.Assignment)
	if err != nil {
		return nil, err
	}
	if source.SQLType != target.SQLType {
		node = plan.CoercionExpr{Info: expr.Info{Usage: target.SQLType.Usage()}, Child: node, Plan: coercion, Mode: sqltype.Assignment}
	}
	return node, nil
}
