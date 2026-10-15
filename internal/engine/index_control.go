package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

type indexPresentation struct {
	dump      bool
	column    int64
	pretty    bool
	path      []ObjectAddress
	temporary ObjectAddress
	// TYPE buffers retain original expression trees and old-Usage decoration.
	reconstruct bool
	output      sqltype.OutputEnvironment
	exclusion   bool
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
			return (pgRenderer{output: presentation.output}).attribute(parent, index.sqlIndex.include[presentation.column-int64(len(index.sqlIndex.keys))-1])
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
	identifier := (pgRenderer{output: presentation.output}).identifier
	if presentation.dump {
		identifier = quoteIdentifier
	}
	schema := catalog.state.descriptors[parent.namespace].Name()
	if !presentation.dump && parent.namespace == presentation.temporary {
		schema = "pg_temp"
	}
	parentName := identifier(schema) + "." + identifier(parent.name)
	if presentation.pretty && !presentation.output.QuoteAllIdentifiers {
		parentName = relationDiagnosticNameAt(catalog, parent, presentation.path)
	}
	// Canonical replay spells the supported access methods as SQL tokens.
	// Session inquiry alone follows quote_all_identifiers.
	method := index.sqlIndex.method.String()
	if !presentation.dump {
		method = identifier(method)
	}
	var sql strings.Builder
	if presentation.exclusion {
		sql.WriteString("EXCLUDE USING " + method + " (")
	} else {
		sql.WriteString("CREATE ")
		if index.sqlIndex.unique {
			sql.WriteString("UNIQUE ")
		}
		only := ""
		if parent.partition != nil {
			only = "ONLY "
		}
		sql.WriteString("INDEX " + identifier(index.name) + " ON " + only + parentName + " USING " + method + " (")
	}
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
			if !presentation.reconstruct {
				actual, err := indexKeyUsage(parent, key)
				if err != nil {
					return "", err
				}
				presented.collation = 0
				if actual.Collation.ID != usage.Collation.ID {
					presented.collation = actual.Collation.ID
				}
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
				sql.WriteString(collationSQLName(catalog, presented.collation))
			} else {
				sql.WriteString(collationDisplayName(catalog, presented.collation, presentation.path))
			}
		}
		if presented.explicitClass {
			sql.WriteByte(' ')
			if presentation.dump {
				sql.WriteString(quoteIdentifier("pg_catalog") + ".")
			}
			sql.WriteString(identifier(presented.class.Name))
		}
		if index.sqlIndex.method != hashMethod {
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
		if presentation.exclusion {
			sql.WriteString(" WITH " + index.sqlIndex.exclusion[i].Name)
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
	if predicate := index.sqlIndex.predicate; predicate != nil {
		var text string
		var err error
		if presentation.dump {
			text, err = (scalarRenderer{owner: parent}).sql(ctx, catalog, predicate.definition)
		} else {
			text, err = (pgRenderer{view: catalog, relation: parent, pretty: presentation.pretty, path: presentation.path, output: presentation.output}).sql(ctx, predicate.definition, pgRenderParent{})
		}
		if err != nil {
			return "", err
		}
		if presentation.exclusion {
			sql.WriteString(" WHERE (" + text + ")")
		} else {
			sql.WriteString(" WHERE " + text)
		}
	}
	if presentation.exclusion {
		c := exclusionDefinition(parent, index)
		if c == nil {
			return "", fmt.Errorf("stuntgres: missing exclusion renderer owner")
		}
		if c.Deferrable {
			sql.WriteString(" DEFERRABLE")
		}
		if c.InitiallyDeferred {
			sql.WriteString(" INITIALLY DEFERRED")
		}
		return sql.String(), nil
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
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		return n.Plan.Name() != "||"
	case sqlJSONExpression, jsonPredicateExpression:
		return true
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin], functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case indexPropertyExpression:
		return true
	case functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties], functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.Timezone[BoundExpr, *EvalContext, ExprProperties]:
		return n.Function
	case functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.ArrayRandom[BoundExpr, *EvalContext, ExprProperties], unnestExpression:
		return false
	case functions.ArrayTransform[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
		return !n.Plan.Membership()
	case functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.RangeOperator[BoundExpr, *EvalContext, ExprProperties]:
		return false
	case functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.Enum[BoundExpr, *EvalContext, ExprProperties], functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties], functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties]:
		return true
	case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
		return !jsonbExistenceName(n.Plan.Name())
	case namespaceInquiryExpression, accessInquiryExpression, objectACLExpression:
		return true
	case functions.Math[BoundExpr, *EvalContext, ExprProperties], functions.Random[BoundExpr, *EvalContext, ExprProperties], advisoryExpression, functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties], functions.NullCount[BoundExpr, *EvalContext, ExprProperties], sequenceCallExpression, currentSchemasExpression, visibilityInquiryExpression, commentInquiryExpression, deparseExpression, conditionalExpression, identityExpression, functions.CurrentDateTime[*EvalContext, ExprProperties], functions.TransactionTime[*EvalContext, ExprProperties]:
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
	if presentation.reconstruct && key.derived != nil {
		node := key.derived.definition
		text, err := (pgRenderer{view: catalog, relation: parent, path: presentation.path, output: presentation.output}).sql(ctx, node, pgRenderParent{})
		return text, !pgIndexFunctionExpression(node), node.ResultUsage(), err
	}
	attribute, node, err := indexCatalogKey(parent, key)
	if err != nil {
		return "", false, sqltype.Usage{}, err
	}
	if node == nil {
		usage, err := indexKeyBaseUsage(parent, indexKey{attribute: attribute})
		if err != nil {
			return "", false, sqltype.Usage{}, err
		}
		text, err := (pgRenderer{output: presentation.output}).attribute(parent, attribute)
		return text, false, usage, err
	}
	text, err := (pgRenderer{view: catalog, relation: parent, pretty: presentation.pretty, path: presentation.path, output: presentation.output}).sql(ctx, node, pgRenderParent{})
	if err != nil {
		return "", false, sqltype.Usage{}, err
	}
	return text, !pgIndexFunctionExpression(node), node.ResultUsage(), nil
}
