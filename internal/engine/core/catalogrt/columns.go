package catalogrt

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

func (r Runtime) ColumnExpression(ctx context.Context, capture catalog.Read, relation *catalog.Relation, expression expr.Expr) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if capture != r.View.Catalog || relation == nil || capture.Get(relation.Address()) != relation {
		return "", ids.ErrCatalogReplaced
	}
	renderer := stored.Deparser{View: capture, Relation: relation, Path: r.Presentation.Path, Output: r.Presentation.Output, NonstandardStrings: r.Presentation.NonstandardStrings}
	return renderer.SQL(ctx, expression, stored.DeparseParent{})
}

func (r Runtime) DomainExpression(ctx context.Context, capture catalog.Read, domain *catalog.Type, expression expr.Expr) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if capture != r.View.Catalog || domain == nil || !domain.SQLType.IsDomain() || capture.Get(domain.Address()) != domain {
		return "", ids.ErrCatalogReplaced
	}
	renderer := stored.Deparser{View: capture, Path: r.Presentation.Path, Output: r.Presentation.Output, NonstandardStrings: r.Presentation.NonstandardStrings}
	return renderer.SQL(ctx, expression, stored.DeparseParent{})
}

func (r Runtime) ColumnUpdatable(ctx context.Context, capture catalog.Read, relation *catalog.Relation, attribute int32) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if capture != r.View.Catalog {
		return false, ids.ErrCatalogReplaced
	}
	return view.ColumnUpdatable(ctx, capture, relation, attribute, func(ctx context.Context, read catalog.Read, relation *catalog.Relation) (bool, bool, error) {
		return fdw.UpdatableEvents(ctx, read, relation, r.restricted())
	})
}
