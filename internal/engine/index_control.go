package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
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

func indexDefinitionSQL(ctx context.Context, view catalog.Read, index *catalog.Relation, presentation indexPresentation) (string, error) {
	if err := validateIndexDefinition(view, index); err != nil {
		return "", err
	}
	parent := view.MustRelation(index.SQLIndex.Parent)
	if presentation.column != 0 {
		if presentation.column < 1 || presentation.column > int64(len(index.Columns)) {
			return "", nil
		}
		if presentation.column > int64(len(index.SQLIndex.Keys)) {
			return (pgRenderer{Output: presentation.output}).Attribute(parent, index.SQLIndex.Include[presentation.column-int64(len(index.SQLIndex.Keys))-1])
		}
		key := index.SQLIndex.Keys[presentation.column-1]
		if !presentation.dump {
			text, wrapped, _, err := compactIndexKeySQL(ctx, view, parent, key, presentation)
			if err != nil {
				return "", err
			}
			if wrapped {
				text = "(" + text + ")"
			}
			return text, nil
		}
		if key.Derived != nil {
			return (scalarRenderer{Owner: parent}).SQL(ctx, view, key.Derived.Definition)
		}
		return DeparseAttribute(parent, key.Attribute)
	}
	identifier := (pgRenderer{Output: presentation.output}).Identifier
	if presentation.dump {
		identifier = catalog.QuoteIdentifier
	}
	schema := view.Get(parent.Namespace()).Name()
	if !presentation.dump && parent.Namespace() == presentation.temporary {
		schema = "pg_temp"
	}
	parentName := identifier(schema) + "." + identifier(parent.Name())
	if presentation.pretty && !presentation.output.QuoteAllIdentifiers {
		parentName = RelationDiagnosticNameAt(view, parent, presentation.path)
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
			text, wrapped, usage, err := compactIndexKeySQL(ctx, view, parent, key, presentation)
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
			text, err := (scalarRenderer{Owner: parent}).SQL(ctx, view, key.Derived.Definition)
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
				sql.WriteString(typedef.CollationSQLName(view, presented.Collation))
			} else {
				sql.WriteString(CollationDisplayName(view, presented.Collation, presentation.path))
			}
		}
		if presented.ExplicitClass {
			sql.WriteByte(' ')
			if presentation.dump {
				sql.WriteString(catalog.QuoteIdentifier("pg_catalog") + ".")
			}
			sql.WriteString(identifier(presented.Class.Name))
		}
		if index.SQLIndex.Method == catalog.BtreeMethod {
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
			text, err = (scalarRenderer{Owner: parent}).SQL(ctx, view, predicate.Definition)
		} else {
			text, err = (pgRenderer{View: view, Relation: parent, Pretty: presentation.pretty, Path: presentation.path, Output: presentation.output}).SQL(ctx, predicate.Definition, pgRenderParent{})
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
	case plan.InstalledUUIDOsspExpr:
		return true
	case plan.UnnestExpr:
		return false
	case plan.ExtensionPathsExpr:
		return true
	case plan.ConditionalExpr:
		return true
	case expr.FunctionSyntax:
		return n.LooksLikeFunction()
	default:
		return false
	}
}

// Compact presentation shares ComputeIndexAttrs classification with metadata,
// the existing TYPE-buffer function grammar, and the decoration reconstruction
// owner. Return the classified column/expression usage, not its type default:
// PostgreSQL compares full-key collation with that usage. Dump stays canonical.
func compactIndexKeySQL(ctx context.Context, view catalog.Read, parent *catalog.Relation, key catalog.IndexKey, presentation indexPresentation) (string, bool, sqltype.Usage, error) {
	if presentation.reconstruct && key.Derived != nil {
		node := key.Derived.Definition
		text, err := (pgRenderer{View: view, Relation: parent, Path: presentation.path, Output: presentation.output}).SQL(ctx, node, pgRenderParent{})
		return text, !pgIndexFunctionExpression(node), node.ResultUsage(), err
	}
	attribute, node, err := indexCatalogKey(parent, key)
	if err != nil {
		return "", false, sqltype.Usage{}, err
	}
	if node == nil {
		usage, err := indexKeyBaseUsage(parent, catalog.IndexKey{Attribute: attribute})
		if err != nil {
			return "", false, sqltype.Usage{}, err
		}
		text, err := (pgRenderer{Output: presentation.output}).Attribute(parent, attribute)
		return text, false, usage, err
	}
	text, err := (pgRenderer{View: view, Relation: parent, Pretty: presentation.pretty, Path: presentation.path, Output: presentation.output}).SQL(ctx, node, pgRenderParent{})
	if err != nil {
		return "", false, sqltype.Usage{}, err
	}
	return text, !pgIndexFunctionExpression(node), node.ResultUsage(), nil
}
