package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

func (r engineCatalogRuntime) ColumnExpression(ctx context.Context, capture catalog.Read, relation *catalog.Relation, expression expr.Expr) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if capture != r.auth.Catalog || relation == nil || capture.Get(relation.Address()) != relation {
		return "", ids.ErrCatalogReplaced
	}
	renderer := stored.Deparser{View: capture, Relation: relation, Path: r.presentation.Path, Output: r.presentation.Output, NonstandardStrings: r.presentation.NonstandardStrings}
	return renderer.SQL(ctx, expression, stored.DeparseParent{})
}

func (r engineCatalogRuntime) ColumnUpdatable(ctx context.Context, capture catalog.Read, relation *catalog.Relation, attribute int32) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if capture != r.auth.Catalog {
		return false, ids.ErrCatalogReplaced
	}
	return view.ColumnUpdatable(ctx, capture, relation, attribute, fdw.UpdatableEvents)
}
