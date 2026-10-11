package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/parser"
)

func viewStringTableAt(root LogicalNode, ordinal int) *stringTableNode {
	if root == nil {
		return nil
	}
	if node, ok := root.(*stringTableNode); ok && node.viewOrdinal == ordinal {
		return node
	}
	left, right := queryInputs(root)
	if found := viewStringTableAt(left, ordinal); found != nil {
		return found
	}
	return viewStringTableAt(right, ordinal)
}
func (v *viewRenderer) renderStringTableSQL(ctx context.Context, catalog CatalogRead, raw *parser.FunctionTable, ns viewNamespace) (viewRenderFrom, error) {
	root := viewStringTableAt(viewFromRoot(v.query.Root), ns.ordinal)
	if root == nil {
		return viewRenderFrom{}, fmt.Errorf("stuntgres: missing frozen string function source")
	}
	texts := make([]string, len(raw.Calls))
	for i, call := range raw.Calls {
		text, err := v.expressionSQL(ctx, catalog, call, viewLiteralPath{clause: viewFunctionLiteral, target: root.viewOrdinal + i})
		if err != nil {
			return viewRenderFrom{}, err
		}
		texts[i] = text
	}
	text := texts[0]
	if raw.RowsFrom {
		text = "ROWS FROM (" + strings.Join(texts, ", ") + ")"
	}
	if raw.Ordinality {
		text += " WITH ORDINALITY"
	}
	if raw.Lateral {
		text = "LATERAL " + text
	}
	from := viewRenderFrom{text: text, relation: text, root: root}
	for i, column := range root.Schema() {
		from.columns = append(from.columns, viewRenderColumn{name: column.Name, slot: i})
	}
	aliased := v.aliasFrom(from, ns, v.namespaceAlias(ns))
	aliased.relation = text
	return aliased, nil
}
