package catalogrt

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// RoutineDefault presents a demanded declaration expression. The producer owns
// disclosure; this capability neither authorizes, evaluates, nor serializes replay.
func (r Runtime) RoutineDefault(ctx context.Context, capture catalog.Read, d *catalog.Procedure, slot int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if capture != r.View.Catalog {
		return "", ids.ErrCatalogReplaced
	}
	if err := validateRoutineDefaultOwner(capture, d); err != nil {
		return "", err
	}
	node, err := r.routineDefaultNode(ctx, capture, d, slot)
	if err != nil {
		return "", err
	}
	renderer := stored.Deparser{View: capture, Path: r.Presentation.Path, Output: r.Presentation.Output, NonstandardStrings: r.Presentation.NonstandardStrings, RoutineDeclaration: true}
	return renderer.SQL(ctx, node, stored.DeparseParent{})
}

// validateRoutineDefaultOwner checks the shared owner once per acquisition, not
// once per element of a potentially large SQL declaration-default list.
func validateRoutineDefaultOwner(capture catalog.Read, d *catalog.Procedure) error {
	if d == nil || capture.Get(d.Address()) != d {
		return ids.ErrCatalogReplaced
	}
	if d.RoutineDefinition() == nil {
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
func (r Runtime) routineDefaultNode(ctx context.Context, capture catalog.Read, d *catalog.Procedure, slot int) (expr.Expr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.RoutineDefinition() != nil {
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
	case sqltype.Bool, sqltype.Int4, sqltype.Int8, sqltype.Float8, sqltype.Numeric, sqltype.Text, sqltype.JSONB, 1009:
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

// RoutineArguments shares exact pg_get_function_arguments presentation between
// routine inquiries and pg_seclabels. It borrows one coherent captured root and
// never executes defaults or grants target invocation authority.
func (r Runtime) RoutineArguments(ctx context.Context, capture catalog.Read, d *catalog.Procedure, defaults bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if capture != r.View.Catalog || d == nil || capture.Get(d.Address()) != d {
		return "", ids.ErrCatalogReplaced
	}
	names, err := pgcatalog.ProcedureNameProjection(capture, d)
	if err != nil {
		return "", err
	}
	renderer := stored.Deparser{View: capture, Path: r.Presentation.Path, Output: r.Presentation.Output, NonstandardStrings: r.Presentation.NonstandardStrings, TemporaryOwner: r.Presentation.TemporaryOwner, RoutineDeclaration: true}
	var out strings.Builder
	written := 0
	var aggregate *catalog.AggregateDefinition
	if d.Aggregate != nil {
		aggregate = &d.Aggregate.Definition
	} else if d.RoutineKind == catalog.AggregateRoutine {
		for i := range pgcatalog.AggregateDefinitions {
			if pgcatalog.AggregateDefinitions[i].Function == d.Address().ObjectID {
				aggregate = &pgcatalog.AggregateDefinitions[i]
				break
			}
		}
		if aggregate == nil {
			return "", fmt.Errorf("stuntgres: missing aggregate declaration")
		}
	}
	ordered := aggregate != nil && aggregate.Kind != "n"
	err = pgcatalog.VisitProcedureFormals(ctx, d, sqltype.Value{}, func(f pgcatalog.ProcedureFormal) error {
		if f.Mode == "t" {
			return nil
		}
		boundary := ordered && written == int(aggregate.DirectArgs)
		if boundary {
			if written > 0 {
				out.WriteByte(' ')
			}
			out.WriteString("ORDER BY ")
		} else if written > 0 {
			out.WriteString(", ")
		}
		start := out.Len()
		switch f.Mode {
		case "i":
			if d.RoutineKind == catalog.ProcedureRoutine {
				out.WriteString("IN ")
			}
		case "o":
			out.WriteString("OUT ")
		case "b":
			out.WriteString("INOUT ")
		case "v":
			out.WriteString("VARIADIC ")
		default:
			return fmt.Errorf("stuntgres: invalid declaration argument mode")
		}
		if name := names.At(f.Index); name != "" {
			out.WriteString(renderer.Identifier(name) + " ")
		}
		if _, ok := capture.LookupType(sqltype.ID(f.Type)); !ok {
			return fmt.Errorf("stuntgres: missing routine declaration type %d", f.Type)
		}
		typ, err := regobj.FormatDeclarationType(capture, r.Presentation.Path, sqltype.ID(f.Type), renderer.Identifier)
		if err != nil {
			return err
		}
		out.WriteString(typ)
		declarationEnd := out.Len()
		if defaults && f.HasDefault {
			text, err := r.RoutineDefault(ctx, capture, d, f.Input)
			if err != nil {
				return err
			}
			out.WriteString(" DEFAULT " + text)
		}
		if ordered && f.Mode == "v" && written+1 == int(aggregate.DirectArgs) && f.Input+1 == int(pgcatalog.ProcedureArity(d)) {
			declaration := out.String()[start:declarationEnd]
			out.WriteString(" ORDER BY ")
			out.WriteString(declaration)
		}
		written++
		return nil
	})
	return out.String(), err
}
