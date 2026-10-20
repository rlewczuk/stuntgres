package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func viewFunctionFromAt(root LogicalNode, ordinal int, selected bool) *functionFromNode {
	if root == nil {
		return nil
	}
	if node, ok := root.(*functionFromNode); ok && node.viewOrdinal == ordinal && node.regexpNamespace == selected {
		return node
	}
	left, right := queryInputs(root)
	if found := viewFunctionFromAt(left, ordinal, selected); found != nil {
		return found
	}
	return viewFunctionFromAt(right, ordinal, selected)
}
func (v *viewRenderer) renderFunctionTableSQL(ctx context.Context, catalog catalog.Read, raw *parser.FunctionTable, ns viewNamespace) (viewRenderFrom, error) {
	root := viewFunctionFromAt(viewFromRoot(v.query.Root), ns.ordinal, ns.node == viewRegexpNamespace)
	if root == nil {
		return viewRenderFrom{}, fmt.Errorf("stuntgres: missing frozen string function source")
	}
	calls := parser.NormalizedFunctionCalls(raw)
	texts := make([]string, len(calls))
	hasDefinitions := false
	for i, call := range calls {
		path := viewLiteralPath{clause: viewFunctionLiteral, target: root.viewOrdinal + i}
		if root.regexpNamespace {
			path.source, path.target = root.viewOrdinal, i
		}
		text, err := v.expressionSQL(ctx, catalog, call, path)
		if err != nil {
			return viewRenderFrom{}, err
		}
		var shape *sqltype.Descriptor
		switch record := root.calls[i].(type) {
		case functions.JSONRecord:
			if record.Defined {
				shape = record.ResultUsage().Type
			}
		case functions.JSONBRecord:
			if record.Definitions {
				shape = record.RecordUsage().Type.BaseType()
			}
		}
		if shape != nil {
			members := make([]string, shape.CompositeLen())
			for j := range members {
				member := shape.CompositeMember(j)
				declaration, err := catalogTypeDeclaration(catalog, member.Usage)
				if err != nil {
					return viewRenderFrom{}, err
				}
				members[j] = v.presentation.identifier(member.Name) + " " + declaration
				if member.Usage.Collation.ID != 0 {
					name := CollationSQLName(catalog, member.Usage.Collation.ID)
					if name == "" {
						return viewRenderFrom{}, fmt.Errorf("stuntgres: missing record member collation")
					}
					members[j] += " COLLATE " + name
				}
			}
			text += " AS (" + strings.Join(members, ", ") + ")"
			hasDefinitions = true
		}
		texts[i] = text
	}
	text := texts[0]
	if raw.RowsFrom || len(calls) > 1 || hasDefinitions {
		text = "ROWS FROM (" + strings.Join(texts, ", ") + ")"
	}
	if raw.Ordinality {
		text += " WITH ORDINALITY"
	}
	if raw.Lateral || root.regexpNamespace && v.presentation.deparse && root.correlated {
		text = "LATERAL " + text
	}
	from := viewRenderFrom{text: text, relation: text, root: root}
	for i, column := range root.Schema() {
		from.columns = append(from.columns, viewRenderColumn{name: column.Name, slot: i})
	}
	if root.regexpNamespace && v.functionSources {
		from.slots = make(map[int]string, len(from.columns))
		qualifier := v.presentation.identifier(v.namespaceAlias(ns))
		for _, column := range from.columns {
			from.slots[column.slot] = qualifier + "." + v.presentation.identifier(column.name)
		}
		v.slots[root] = from.slots
		return from, nil
	}
	aliased := v.aliasFrom(from, ns, v.namespaceAlias(ns))
	aliased.relation = text
	return aliased, nil
}
