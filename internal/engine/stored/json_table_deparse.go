package stored

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
)

func (v *QueryRenderer) renderJSONTableSQL(ctx context.Context, view catalog.Read, raw *parser.JSONTable, ns analyze.ViewNamespace, name ruleRangeName) (viewRenderFrom, error) {
	root := viewFunctionFromAt(analyze.ViewFromRoot(v.query.Root), ns.Ordinal, true)
	if root == nil || len(root.Calls) != 1 {
		return viewRenderFrom{}, fmt.Errorf("stuntgres: missing frozen JSON_TABLE source")
	}
	table, ok := root.Calls[0].(functions.JSONTable)
	if !ok {
		return viewRenderFrom{}, fmt.Errorf("stuntgres: invalid frozen JSON_TABLE source")
	}
	written := raw.Expressions()
	parts := make([]string, len(written))
	for i, child := range written {
		text, err := v.ExpressionSQL(ctx, view, child, analyze.ViewLiteralPath{Clause: analyze.ViewFunctionLiteral, Source: root.ViewOrdinal, Target: i})
		if err != nil {
			return viewRenderFrom{}, err
		}
		parts[i] = text
	}
	quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }
	path := func(raw parser.Expr) string {
		if raw == nil {
			return ""
		}
		return quote(raw.(parser.LiteralExpr).Value.Text)
	}
	pathName := func(name *parser.Identifier) string {
		if name == nil {
			return ""
		}
		return " AS " + catalog.QuoteIdentifier(name.Text)
	}
	var out strings.Builder
	out.WriteString("JSON_TABLE(")
	out.WriteString(parts[0])
	out.WriteString(functions.JSONFormatSQL(raw.Input))
	out.WriteString(", ")
	out.WriteString(path(raw.Path))
	out.WriteString(pathName(raw.PathName))
	for i, p := range raw.Passing {
		if i == 0 {
			out.WriteString(" PASSING ")
		} else {
			out.WriteString(", ")
		}
		out.WriteString(parts[i+1])
		out.WriteString(" AS ")
		out.WriteString(catalog.QuoteIdentifier(p.Name.Text))
	}
	next := 1 + len(raw.Passing)
	var columns func([]parser.JSONTableColumn, functions.JSONTableLevel) error
	columns = func(raw []parser.JSONTableColumn, level functions.JSONTableLevel) error {
		out.WriteString(" COLUMNS (")
		regular, nested := 0, 0
		for i, c := range raw {
			if i > 0 {
				out.WriteString(", ")
			}
			if len(c.Columns) > 0 {
				out.WriteString("NESTED PATH ")
				out.WriteString(path(c.Path))
				out.WriteString(pathName(c.PathName))
				if err := columns(c.Columns, level.Nested[nested]); err != nil {
					return err
				}
				nested++
				continue
			}
			field := level.Columns[regular]
			regular++
			out.WriteString(catalog.QuoteIdentifier(c.Name.Text))
			if c.Ordinality {
				out.WriteString(" FOR ORDINALITY")
				continue
			}
			declaration, err := regobj.TypeDeclaration(view, table.ResultUsage().Type.CompositeMember(field.Slot).Usage)
			if err != nil {
				return err
			}
			out.WriteString(" ")
			out.WriteString(declaration)
			out.WriteString(functions.JSONFormatSQL(c.Format))
			if c.Exists {
				out.WriteString(" EXISTS")
			}
			if c.Path != nil {
				out.WriteString(" PATH ")
				out.WriteString(path(c.Path))
			}
			switch c.Wrapper {
			case "without":
				out.WriteString(" WITHOUT WRAPPER")
			case "conditional":
				out.WriteString(" WITH CONDITIONAL WRAPPER")
			case "unconditional":
				out.WriteString(" WITH UNCONDITIONAL WRAPPER")
			}
			if c.Quotes != "" {
				out.WriteString(" " + strings.ToUpper(c.Quotes) + " QUOTES")
			}
			for j, b := range []parser.SQLJSONBehavior{c.Empty, c.Error} {
				if b.Kind == "" {
					continue
				}
				out.WriteString(" " + strings.ToUpper(b.Kind))
				if b.Default != nil {
					out.WriteString(" ")
					out.WriteString(parts[next])
					next++
				}
				if j == 0 {
					out.WriteString(" ON EMPTY")
				} else {
					out.WriteString(" ON ERROR")
				}
			}
		}
		out.WriteString(")")
		return nil
	}
	if err := columns(raw.Columns, table.Level); err != nil {
		return viewRenderFrom{}, err
	}
	if raw.Error.Kind != "" {
		out.WriteString(" " + strings.ToUpper(raw.Error.Kind) + " ON ERROR")
	}
	out.WriteString(")")
	text := out.String()
	if raw.Lateral || v.Presentation.Deparse && root.Correlated {
		text = "LATERAL " + text
	}
	from := viewRenderFrom{text: text, relation: text, root: root}
	for i, column := range root.Schema() {
		from.columns = append(from.columns, viewRenderColumn{name: column.Name, slot: i})
	}
	if v.Presentation.Rule {
		from = v.sourceAlias(from, name.name, name.columns, name.explicit || name.name != "json_table" || len(name.columns) != 0)
		from.relation = text
		return from, nil
	}
	if v.Presentation.Deparse {
		from.slots = make(map[int]string, len(from.columns))
		qualifier := v.Presentation.identifier(v.namespaceAlias(ns))
		for _, column := range from.columns {
			from.slots[column.slot] = qualifier + "." + v.Presentation.identifier(column.name)
		}
		v.Slots[root] = from.slots
		return from, nil
	}
	aliased := v.aliasFrom(from, ns, v.namespaceAlias(ns))
	aliased.relation = text
	return aliased, nil
}
