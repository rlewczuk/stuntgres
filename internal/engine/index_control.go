package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type indexPresentation struct {
	dump      bool
	column    int64
	pretty    bool
	path      []ids.ObjectAddress
	temporary ids.ObjectAddress
	// TYPE buffers retain original expression trees and old-Usage decoration.
	reconstruct bool
	output      sqltype.OutputEnvironment
	exclusion   bool
}

func indexDefinitionSQL(ctx context.Context, catalog CatalogRead, index *relationDescriptor, presentation indexPresentation) (string, error) {
	if err := validateIndexDefinition(catalog, index); err != nil {
		return "", err
	}
	parent := catalog.MustRelation(index.SQLIndex.Parent)
	if presentation.column != 0 {
		if presentation.column < 1 || presentation.column > int64(len(index.Columns)) {
			return "", nil
		}
		if presentation.column > int64(len(index.SQLIndex.Keys)) {
			return (pgRenderer{output: presentation.output}).attribute(parent, index.SQLIndex.Include[presentation.column-int64(len(index.SQLIndex.Keys))-1])
		}
		key := index.SQLIndex.Keys[presentation.column-1]
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
		if key.Derived != nil {
			return (scalarRenderer{owner: parent}).sql(ctx, catalog, key.Derived.Definition)
		}
		return deparseAttribute(parent, key.Attribute)
	}
	identifier := (pgRenderer{output: presentation.output}).identifier
	if presentation.dump {
		identifier = quoteIdentifier
	}
	schema := catalog.Get(parent.Namespace()).Name()
	if !presentation.dump && parent.Namespace() == presentation.temporary {
		schema = "pg_temp"
	}
	parentName := identifier(schema) + "." + identifier(parent.Name())
	if presentation.pretty && !presentation.output.QuoteAllIdentifiers {
		parentName = relationDiagnosticNameAt(catalog, parent, presentation.path)
	}
	// Canonical replay spells the supported access methods as SQL tokens.
	// Session inquiry alone follows quote_all_identifiers.
	method := index.SQLIndex.Method.String()
	if !presentation.dump {
		method = identifier(method)
	}
	var sql strings.Builder
	if presentation.exclusion {
		sql.WriteString("EXCLUDE USING " + method + " (")
	} else {
		sql.WriteString("CREATE ")
		if index.SQLIndex.Unique {
			sql.WriteString("UNIQUE ")
		}
		only := ""
		if parent.Partition != nil {
			only = "ONLY "
		}
		sql.WriteString("INDEX " + identifier(index.Name()) + " ON " + only + parentName + " USING " + method + " (")
	}
	for i, key := range index.SQLIndex.Keys {
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
			presented = reconstructIndexKey(key, usage, usage, index.SQLIndex.Method)
			if !presentation.reconstruct {
				actual, err := indexKeyUsage(parent, key)
				if err != nil {
					return "", err
				}
				presented.Collation = 0
				if actual.Collation.ID != usage.Collation.ID {
					presented.Collation = actual.Collation.ID
				}
			}
		} else if key.Derived != nil {
			text, err := (scalarRenderer{owner: parent}).sql(ctx, catalog, key.Derived.Definition)
			if err != nil {
				return "", err
			}
			sql.WriteString("(" + text + ")")
		} else {
			slot := -1
			for j, attribute := range parent.Attributes {
				if attribute == key.Attribute {
					slot = j
					break
				}
			}
			if slot < 0 {
				return "", fmt.Errorf("stuntgres: index attribute is missing")
			}
			sql.WriteString(identifier(parent.Columns[slot].Name))
		}
		if presented.Collation != 0 {
			sql.WriteString(" COLLATE ")
			if presentation.dump {
				sql.WriteString(collationSQLName(catalog, presented.Collation))
			} else {
				sql.WriteString(collationDisplayName(catalog, presented.Collation, presentation.path))
			}
		}
		if presented.ExplicitClass {
			sql.WriteByte(' ')
			if presentation.dump {
				sql.WriteString(quoteIdentifier("pg_catalog") + ".")
			}
			sql.WriteString(identifier(presented.Class.Name))
		}
		if index.SQLIndex.Method == btreeMethod {
			if key.Desc {
				sql.WriteString(" DESC")
			} else if presentation.dump {
				sql.WriteString(" ASC")
			}
			if presentation.dump || key.NullsFirst != key.Desc {
				if key.NullsFirst {
					sql.WriteString(" NULLS FIRST")
				} else {
					sql.WriteString(" NULLS LAST")
				}
			}
		}
		if presentation.exclusion {
			sql.WriteString(" WITH " + index.SQLIndex.Exclusion[i].Name)
		}
	}
	sql.WriteByte(')')
	if len(index.SQLIndex.Include) != 0 {
		sql.WriteString(" INCLUDE (")
		for i, attribute := range index.SQLIndex.Include {
			if i > 0 {
				sql.WriteString(", ")
			}
			slot := slices.Index(parent.Attributes, attribute)
			if slot < 0 {
				return "", fmt.Errorf("stuntgres: index included attribute is missing")
			}
			sql.WriteString(identifier(parent.Columns[slot].Name))
		}
		sql.WriteByte(')')
	}
	if index.SQLIndex.NullsNotDistinct {
		sql.WriteString(" NULLS NOT DISTINCT")
	}
	if predicate := index.SQLIndex.Predicate; predicate != nil {
		var text string
		var err error
		if presentation.dump {
			text, err = (scalarRenderer{owner: parent}).sql(ctx, catalog, predicate.Definition)
		} else {
			text, err = (pgRenderer{view: catalog, relation: parent, pretty: presentation.pretty, path: presentation.path, output: presentation.output}).sql(ctx, predicate.Definition, pgRenderParent{})
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
func pgIndexFunctionExpression(node expr.Expr) bool {
	switch n := node.(type) {
	case functions.String:
		return n.Plan.Name() != "||"
	case functions.SQLJSON, functions.JSONPredicate:
		return true
	case functions.StringVariadic, functions.JSONCreate:
		return true
	case indexPropertyExpression:
		return true
	case functions.DatetimeJustify:
		return true
	case functions.DatetimeExtract:
		return true
	case functions.DatetimeOverlaps:
		return true
	case functions.DatetimeTrunc, functions.DatetimeBin:
		return true
	case functions.DatetimeZoneCalendar:
		return true
	case functions.Timezone:
		return n.Function
	case functions.DatetimeAgeFinite:
		return true
	case functions.JSONStripNulls:
		return true
	case functions.JSONExtract:
		return true
	case functions.JSONBPathQueryArray:
		return true
	case functions.JSONBPathExists:
		return true
	case functions.JSONBPathMatch:
		return true
	case functions.JSONBPathQueryFirst:
		return true
	case functions.JSONBSet:
		return true
	case functions.DatetimeMake:
		return true
	case functions.ArrayRandom, unnestExpression:
		return false
	case functions.ArrayTransform:
		return true
	case functions.ArrayInspect:
		return true
	case functions.ArraySearch:
		return !n.Plan.Membership()
	case aclValueExpression:
		return n.kind != aclContains
	case catalogUserNameExpression, catalogForeignKeysExpression:
		return true
	case transactionIdentityExpression:
		return true
	case commitTimestampExpression:
		return true
	case functions.UUIDExtract:
		return true
	case functions.NetworkFormat:
		return true
	case functions.RangeOperator:
		return false
	case functions.RangeInspect:
		return true
	case functions.Enum, functions.PosixSubstring, functions.Regexp:
		return true
	case functions.DatetimeSleep:
		return true
	case functions.Binary:
		return !functions.JSONBExistenceName(n.Plan.Name()) && !functions.JSONBContainmentName(n.Plan.Name())
	case namespaceInquiryExpression, blockingInquiryExpression, accessInquiryExpression, objectACLExpression, objectAddressExpression:
		return true
	case functions.VersionInformation:
		return true
	case functions.Math, functions.Random, advisoryExpression, functions.UUIDGeneration, functions.NullCount, sequenceCallExpression, currentSchemasExpression, visibilityInquiryExpression, commentInquiryExpression, deparseExpression, conditionalExpression, identityExpression, functions.CurrentDateTime, functions.TransactionTime:
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
	if presentation.reconstruct && key.Derived != nil {
		node := key.Derived.Definition
		text, err := (pgRenderer{view: catalog, relation: parent, path: presentation.path, output: presentation.output}).sql(ctx, node, pgRenderParent{})
		return text, !pgIndexFunctionExpression(node), node.ResultUsage(), err
	}
	attribute, node, err := indexCatalogKey(parent, key)
	if err != nil {
		return "", false, sqltype.Usage{}, err
	}
	if node == nil {
		usage, err := indexKeyBaseUsage(parent, indexKey{Attribute: attribute})
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
