package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

type indexPresentation struct {
	dump   bool
	column int64
	pretty bool
	path   []ObjectAddress
}

func indexDefinitionSQL(ctx context.Context, catalog CatalogRead, index *relationDescriptor, presentation indexPresentation) (string, error) {
	if err := validateIndexDefinition(catalog, index); err != nil {
		return "", err
	}
	parent := catalog.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	expressionSQL := func(key indexKey) (string, error) {
		if presentation.dump {
			return (scalarRenderer{owner: parent}).sql(ctx, catalog, key.derived.definition)
		}
		text, err := (pgRenderer{view: catalog, relation: parent, pretty: presentation.pretty, path: presentation.path}).sql(ctx, key.derived.definition, pgRenderParent{})
		if err != nil {
			return "", err
		}
		// Index expression grouping applies to both full and attribute-only
		// inquiries; pretty rendering may already omit the operator's inner group.
		if !pgIndexFunctionExpression(key.derived.definition) {
			text = "(" + text + ")"
		}
		return text, nil
	}
	if presentation.column != 0 {
		if presentation.column < 1 || presentation.column > int64(len(index.columns)) {
			return "", nil
		}
		if presentation.column > int64(len(index.sqlIndex.keys)) {
			return deparseAttribute(parent, index.sqlIndex.include[presentation.column-int64(len(index.sqlIndex.keys))-1])
		}
		key := index.sqlIndex.keys[presentation.column-1]
		if key.derived != nil {
			return expressionSQL(key)
		}
		return deparseAttribute(parent, key.attribute)
	}
	identifier := pgIdentifier
	if presentation.dump {
		identifier = quoteIdentifier
	}
	schema := catalog.state.descriptors[parent.namespace].Name()
	parentName := identifier(schema) + "." + identifier(parent.name)
	if presentation.pretty {
		parentName = relationDiagnosticNameAt(catalog, parent, presentation.path)
	}
	var sql strings.Builder
	sql.WriteString("CREATE ")
	if index.sqlIndex.unique != nil {
		sql.WriteString("UNIQUE ")
	}
	sql.WriteString("INDEX " + identifier(index.name) + " ON " + parentName + " USING btree (")
	for i, key := range index.sqlIndex.keys {
		if i > 0 {
			sql.WriteString(", ")
		}
		if key.derived != nil {
			text, err := expressionSQL(key)
			if err != nil {
				return "", err
			}
			if !presentation.dump {
				sql.WriteString(text)
			} else {
				sql.WriteString("(" + text + ")")
			}
		} else {
			slot := -1
			for j, attribute := range parent.attributes {
				if attribute == key.attribute {
					slot = j
					break
				}
			}
			if slot < 0 {
				return "", fmt.Errorf("stuntgres: index attribute is missing")
			}
			sql.WriteString(identifier(parent.columns[slot].Name))
		}
		presented := key
		if !presentation.dump {
			usage, err := indexKeyBaseUsage(parent, key)
			if err != nil {
				return "", err
			}
			presented = reconstructIndexKey(key, usage, usage)
		}
		if presented.collation != 0 {
			sql.WriteString(" COLLATE ")
			if presentation.dump {
				sql.WriteString(quoteIdentifier("pg_catalog") + ".")
			}
			sql.WriteString(identifier(builtinCollationName(presented.collation)))
		}
		if presented.explicitClass {
			sql.WriteByte(' ')
			if presentation.dump {
				sql.WriteString(quoteIdentifier("pg_catalog") + ".")
			}
			sql.WriteString(identifier(presented.class.Name))
		}
		if key.desc {
			sql.WriteString(" DESC")
		} else if presentation.dump {
			sql.WriteString(" ASC")
		}
		if presentation.dump || key.nullsFirst != key.desc {
			if key.nullsFirst {
				sql.WriteString(" NULLS FIRST")
			} else {
				sql.WriteString(" NULLS LAST")
			}
		}
	}
	sql.WriteByte(')')
	if len(index.sqlIndex.include) != 0 {
		sql.WriteString(" INCLUDE (")
		for i, attribute := range index.sqlIndex.include {
			if i > 0 {
				sql.WriteString(", ")
			}
			slot := slices.Index(parent.attributes, attribute)
			if slot < 0 {
				return "", fmt.Errorf("stuntgres: index included attribute is missing")
			}
			sql.WriteString(identifier(parent.columns[slot].Name))
		}
		sql.WriteByte(')')
	}
	if index.sqlIndex.unique != nil && index.sqlIndex.unique.NullsNotDistinct {
		sql.WriteString(" NULLS NOT DISTINCT")
	}
	if presentation.dump {
		sql.WriteString(";\n")
	}
	return sql.String(), nil
}

// PostgreSQL looks_like_function accepts func_expr_windowless, including its
// SQL-special COALESCE/NULLIF/MinMax and SQL-value forms, not only FuncExpr.
// Inquiry and original TYPE buffers share this rule; canonical Dump does not.
func pgIndexFunctionExpression(node BoundExpr) bool {
	switch n := node.(type) {
	case stringExpression:
		return n.plan.Name() != "||"
	case indexPropertyExpression:
		return true
	case mathExpression, binaryExpression, randomExpression, nullCountExpression, sequenceCallExpression, currentSchemasExpression, visibilityInquiryExpression, commentInquiryExpression, deparseExpression, conditionalExpression, identityExpression, currentDateTimeExpression:
		return true
	default:
		return false
	}
}
