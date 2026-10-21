package stored

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
)

// The first subscripts argument must supply a typed array on replay. Frozen
// array literals already render their owning cast; a matching source cast must
// not wrap that same typed leaf again on every Dump/Restore round trip. Keep
// differing casts and their original traversal ordinals intact. This applies
// only to subscripts dump arguments, not general stored-expression rendering.
func (v *QueryRenderer) subscriptsArraySQLAt(ctx context.Context, catalog catalog.Read, raw parser.Expr, path analyze.ViewLiteralPath, ordinal *int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path.Node = *ordinal
	cast, isCast := raw.(parser.CastExpr)
	usage, captured := v.castUsages[path]
	if !isCast || !captured || !usage.Type.BaseType().IsArray() {
		return v.ExpressionSQLAt(ctx, catalog, raw, path, ordinal, DeparseParent{functionArgument: true})
	}
	(*ordinal)++
	literalPath := path
	literalPath.Node = *ordinal
	text, err := v.subscriptsArraySQLAt(ctx, catalog, cast.Child, path, ordinal)
	if err != nil {
		return "", err
	}
	if literal, ok := v.literals[literalPath]; ok && literal.ResultUsage() == usage {
		return text, nil
	}
	declaration, err := regobj.TypeDeclaration(catalog, usage)
	if err != nil {
		return "", err
	}
	return "CAST(" + text + " AS " + declaration + ")", nil
}
