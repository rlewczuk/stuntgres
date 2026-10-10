package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/sqltype"
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
	if presentation.column != 0 {
		if presentation.column < 1 || presentation.column > int64(len(index.columns)) {
			return "", nil
		}
		if presentation.column > int64(len(index.sqlIndex.keys)) {
			return deparseAttribute(parent, index.sqlIndex.include[presentation.column-int64(len(index.sqlIndex.keys))-1])
		}
		key := index.sqlIndex.keys[presentation.column-1]
		if !presentation.dump {
			text, wrapped, _, err := compactIndexKeySQL(ctx, catalog, parent, key, presentation)
			if err != nil {
				return "", err
			}
			if wrapped {
				text = "(" + text + ")"
			}
			return text, nil
		}
		if key.derived != nil {
			return (scalarRenderer{owner: parent}).sql(ctx, catalog, key.derived.definition)
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
	if index.sqlIndex.unique {
		sql.WriteString("UNIQUE ")
	}
	sql.WriteString("INDEX " + identifier(index.name) + " ON " + parentName + " USING " + index.sqlIndex.method.String() + " (")
	for i, key := range index.sqlIndex.keys {
		if i > 0 {
			sql.WriteString(", ")
		}
		presented := key
		if !presentation.dump {
			text, wrapped, usage, err := compactIndexKeySQL(ctx, catalog, parent, key, presentation)
			if err != nil {
				return "", err
			}
			if wrapped {
				sql.WriteByte('(')
			}
			sql.WriteString(text)
			if wrapped {
				sql.WriteByte(')')
			}
			presented = reconstructIndexKey(key, usage, usage, index.sqlIndex.method)
			actual, err := indexKeyUsage(parent, key)
			if err != nil {
				return "", err
			}
			presented.collation = 0
			if actual.Collation.ID != usage.Collation.ID {
				presented.collation = actual.Collation.ID
			}
		} else if key.derived != nil {
			text, err := (scalarRenderer{owner: parent}).sql(ctx, catalog, key.derived.definition)
			if err != nil {
				return "", err
			}
			sql.WriteString("(" + text + ")")
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
		if index.sqlIndex.method == hashMethod {
			continue
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
	if index.sqlIndex.nullsNotDistinct {
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
	case datetimeJustifyExpression:
		return true
	case datetimeAgeFiniteExpression:
		return true
	case datetimeMakeExpression:
		return true
	case mathExpression, binaryExpression, randomExpression, nullCountExpression, sequenceCallExpression, currentSchemasExpression, visibilityInquiryExpression, commentInquiryExpression, deparseExpression, conditionalExpression, identityExpression, currentDateTimeExpression:
		return true
	default:
		return false
	}
}

// Compact presentation shares ComputeIndexAttrs classification with metadata,
// the existing TYPE-buffer function grammar, and the decoration reconstruction
// owner. Return the classified column/expression usage, not its type default:
// PostgreSQL compares full-key collation with that usage. Dump stays canonical.
func compactIndexKeySQL(ctx context.Context, catalog CatalogRead, parent *relationDescriptor, key indexKey, presentation indexPresentation) (string, bool, sqltype.Usage, error) {
	attribute, node, err := indexCatalogKey(parent, key)
	if err != nil {
		return "", false, sqltype.Usage{}, err
	}
	if node == nil {
		usage, err := indexKeyBaseUsage(parent, indexKey{attribute: attribute})
		if err != nil {
			return "", false, sqltype.Usage{}, err
		}
		text, err := deparseAttribute(parent, attribute)
		return text, false, usage, err
	}
	text, err := (pgRenderer{view: catalog, relation: parent, pretty: presentation.pretty, path: presentation.path}).sql(ctx, node, pgRenderParent{})
	if err != nil {
		return "", false, sqltype.Usage{}, err
	}
	return text, !pgIndexFunctionExpression(node), node.ResultUsage(), nil
}
