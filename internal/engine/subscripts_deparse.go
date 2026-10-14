package engine

import (
	"context"

	"stuntgres/internal/parser"
)

// The first subscripts argument must supply a typed array on replay. Frozen
// array literals already render their owning cast; a matching source cast must
// not wrap that same typed leaf again on every Dump/Restore round trip. Keep
// differing casts and their original traversal ordinals intact. This applies
// only to subscripts dump arguments, not general stored-expression rendering.
func (v *viewRenderer) subscriptsArraySQLAt(ctx context.Context, catalog CatalogRead, raw parser.Expr, path viewLiteralPath, ordinal *int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path.node = *ordinal
	cast, isCast := raw.(parser.CastExpr)
	usage, captured := v.castUsages[path]
	if !isCast || !captured || !usage.Type.BaseType().IsArray() {
		return v.expressionSQLAt(ctx, catalog, raw, path, ordinal, pgRenderParent{functionArgument: true})
	}
	(*ordinal)++
	literalPath := path
	literalPath.node = *ordinal
	text, err := v.subscriptsArraySQLAt(ctx, catalog, cast.Child, path, ordinal)
	if err != nil {
		return "", err
	}
	if literal, ok := v.literals[literalPath]; ok && literal.ResultUsage() == usage {
		return text, nil
	}
	declaration, err := catalogTypeDeclaration(catalog, usage)
	if err != nil {
		return "", err
	}
	return "CAST(" + text + " AS " + declaration + ")", nil
}
