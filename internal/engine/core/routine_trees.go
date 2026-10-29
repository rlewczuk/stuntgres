package core

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// RoutineTrees captures immutable declaration owners only. This capability does
// not authorize disclosure, compile a program, evaluate defaults or open a body.
func (r EngineCatalogRuntime) RoutineTrees(ctx context.Context, capture catalog.Read, d *catalog.Procedure, typ *sqltype.Descriptor) (defaults, body sqltype.Value, err error) {
	if err = ctx.Err(); err != nil {
		return sqltype.Value{}, sqltype.Value{}, err
	}
	if d == nil || capture.Get(d.Address()) != d {
		return sqltype.Value{}, sqltype.Value{}, ids.ErrCatalogReplaced
	}
	if typ == nil || typ.ID() != sqltype.PgNodeTree {
		return sqltype.Value{}, sqltype.Value{}, fmt.Errorf("stuntgres: invalid routine tree type")
	}
	defaults, body = sqltype.Null(typ), sqltype.Null(typ)
	count := int(d.Installed.Defaults)
	if d.Builtin != nil {
		count = int(d.Builtin.Defaults)
	}
	if d.RoutineDefinition() != nil {
		count = d.RoutineDefinition().DefaultCount()
	}
	if count != 0 {
		if err = validateRoutineDefaultOwner(capture, d); err != nil {
			return sqltype.Value{}, sqltype.Value{}, err
		}
		inputs := 0
		if d.Signature != "" {
			inputs = strings.Count(string(d.Signature), ",") + 1
		}
		if count > inputs {
			return sqltype.Value{}, sqltype.Value{}, fmt.Errorf("stuntgres: invalid routine default count")
		}
		nodes := make([]expr.Expr, count)
		for i := range nodes {
			nodes[i], err = r.routineDefaultNode(ctx, capture, d, inputs-count+i)
			if err != nil {
				return sqltype.Value{}, sqltype.Value{}, err
			}
		}
		defaults, err = sqltype.Tree(typ, plan.OwnExpressionListTree(nodes))
		if err != nil {
			return sqltype.Value{}, sqltype.Value{}, err
		}
	}
	if d.SQL != nil && d.SQL.Standard {
		if d.SQLBody == nil || d.DeclarationBody != nil {
			return sqltype.Value{}, sqltype.Value{}, fmt.Errorf("stuntgres: missing checked routine body")
		}
		if err = d.SQLBody.ValidateDefinition(d); err != nil {
			return sqltype.Value{}, sqltype.Value{}, err
		}
		if err = d.SQLBody.ValidateMetadata(capture); err != nil {
			return sqltype.Value{}, sqltype.Value{}, err
		}
		body, err = sqltype.Tree(typ, d.SQLBody)
	} else if d.DeclarationBody != nil {
		if d.RoutineDefinition() != nil {
			return sqltype.Value{}, sqltype.Value{}, fmt.Errorf("stuntgres: invalid quoted routine body authority")
		}
		body, err = sqltype.Tree(typ, d.DeclarationBody)
	}
	if err != nil {
		return sqltype.Value{}, sqltype.Value{}, err
	}
	if err = ctx.Err(); err != nil {
		return sqltype.Value{}, sqltype.Value{}, err
	}
	return defaults, body, nil
}
