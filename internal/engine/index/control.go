package index

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type Presentation struct {
	Dump      bool
	Column    int64
	Pretty    bool
	Path      []ids.ObjectAddress
	Temporary ids.ObjectAddress
	// TYPE buffers retain original expression trees and old-Usage decoration.
	Reconstruct bool
	Output      sqltype.OutputEnvironment
	Exclusion   bool
}

func DefinitionSQL(ctx context.Context, view catalog.Read, index *catalog.Relation, presentation Presentation) (string, error) {
	if err := ValidateIndexDefinition(view, index); err != nil {
		return "", err
	}
	parent := view.MustRelation(index.SQLIndex.Parent)
	if presentation.Column != 0 {
		if presentation.Column < 1 || presentation.Column > int64(len(index.Columns)) {
			return "", nil
		}
		if presentation.Column > int64(len(index.SQLIndex.Keys)) {
			return (stored.Deparser{Output: presentation.Output}).Attribute(parent, index.SQLIndex.Include[presentation.Column-int64(len(index.SQLIndex.Keys))-1])
		}
		key := index.SQLIndex.Keys[presentation.Column-1]
		if !presentation.Dump {
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
			return (stored.ScalarRenderer{Owner: parent}).SQL(ctx, view, key.Derived.Definition)
		}
		return stored.DeparseAttribute(parent, key.Attribute)
	}
	identifier := (stored.Deparser{Output: presentation.Output}).Identifier
	if presentation.Dump {
		identifier = catalog.QuoteIdentifier
	}
	schema := view.Get(parent.Namespace()).Name()
	if !presentation.Dump && parent.Namespace() == presentation.Temporary {
		schema = "pg_temp"
	}
	parentName := identifier(schema) + "." + identifier(parent.Name())
	if presentation.Pretty && !presentation.Output.QuoteAllIdentifiers {
		parentName = stored.RelationDiagnosticNameAt(view, parent, presentation.Path)
	}
	// Builtin tokens retain canonical spelling; custom identifiers are quoted.
	methodFact, ok := view.AccessMethod(index.SQLIndex.MethodIdentity())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing index method identity")
	}
	method := catalog.Identifier(methodFact.Name())
	if !presentation.Dump {
		method = identifier(methodFact.Name())
	}
	var sql strings.Builder
	if presentation.Exclusion {
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
		if !presentation.Dump {
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
			presented = ReconstructIndexKey(view, key, usage, usage, index.SQLIndex.MethodIdentity())
			if !presentation.Reconstruct {
				actual, err := KeyUsage(parent, key)
				if err != nil {
					return "", err
				}
				presented.Collation = 0
				if actual.Collation.ID != usage.Collation.ID {
					presented.Collation = actual.Collation.ID
				}
			}
		} else if key.Derived != nil {
			text, err := (stored.ScalarRenderer{Owner: parent}).SQL(ctx, view, key.Derived.Definition)
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
			if presentation.Dump {
				sql.WriteString(typedef.CollationSQLName(view, presented.Collation))
			} else {
				sql.WriteString(stored.CollationDisplayName(view, presented.Collation, presentation.Path))
			}
		}
		if presented.ExplicitClass {
			class, exists := view.OperatorClass(ids.OID(presented.Class.OID()))
			if !exists {
				return "", fmt.Errorf("stuntgres: missing index operator class")
			}
			schema, exists := view.Namespace(class.Namespace())
			if !exists {
				return "", fmt.Errorf("stuntgres: missing operator class namespace")
			}
			sql.WriteByte(' ')
			if presentation.Dump || class.Namespace().ObjectID != ids.PGCatalogNamespaceOID {
				sql.WriteString(identifier(schema.Name()) + ".")
			}
			sql.WriteString(identifier(class.Name()))
		}
		if index.SQLIndex.Method == catalog.BtreeMethod {
			if key.Desc {
				sql.WriteString(" DESC")
			} else if presentation.Dump {
				sql.WriteString(" ASC")
			}
			if presentation.Dump || key.NullsFirst != key.Desc {
				if key.NullsFirst {
					sql.WriteString(" NULLS FIRST")
				} else {
					sql.WriteString(" NULLS LAST")
				}
			}
		}
		if presentation.Exclusion {
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
	if !presentation.Dump && len(index.StorageOptions.Base) != 0 {
		sql.WriteString(" WITH (" + OptionsSQL(index.StorageOptions.Base) + ")")
	}
	if predicate := index.SQLIndex.Predicate; predicate != nil {
		var text string
		var err error
		if presentation.Dump {
			text, err = (stored.ScalarRenderer{Owner: parent}).SQL(ctx, view, predicate.Definition)
		} else {
			text, err = (stored.Deparser{View: view, Relation: parent, Pretty: presentation.Pretty, Path: presentation.Path, Output: presentation.Output}).SQL(ctx, predicate.Definition, stored.DeparseParent{})
		}
		if err != nil {
			return "", err
		}
		if presentation.Exclusion {
			sql.WriteString(" WHERE (" + text + ")")
		} else {
			sql.WriteString(" WHERE " + text)
		}
	}
	if presentation.Exclusion {
		c := ExclusionDefinition(parent, index)
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
	if presentation.Dump {
		sql.WriteString(";\n")
	}
	return sql.String(), nil
}

// OptionsSQL renders reloption values like PostgreSQL's flatten_reloptions:
// identifier-safe values stay bare, all others are SQL string literals.
// It is also accepted by ALTER INDEX replay, without widening CREATE WITH.
func OptionsSQL(options []catalog.RelationOption) string {
	var sql strings.Builder
	for i, option := range options {
		if i != 0 {
			sql.WriteString(", ")
		}
		sql.WriteString(catalog.Identifier(option.Name))
		sql.WriteByte('=')
		if parser.IdentifierNeedsQuotes(option.Text) {
			sql.WriteString("'" + strings.ReplaceAll(option.Text, "'", "''") + "'")
		} else {
			sql.WriteString(option.Text)
		}
	}
	return sql.String()
}

// PostgreSQL looks_like_function accepts func_expr_windowless, including its
// SQL-special COALESCE/NULLIF/MinMax and SQL-value forms, not only FuncExpr.
// Inquiry and original TYPE buffers share this rule; canonical Dump does not.
func PGIndexFunctionExpression(node expr.Expr) bool {
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
func compactIndexKeySQL(ctx context.Context, view catalog.Read, parent *catalog.Relation, key catalog.IndexKey, presentation Presentation) (string, bool, sqltype.Usage, error) {
	if presentation.Reconstruct && key.Derived != nil {
		node := key.Derived.Definition
		text, err := (stored.Deparser{View: view, Relation: parent, Path: presentation.Path, Output: presentation.Output}).SQL(ctx, node, stored.DeparseParent{})
		return text, !PGIndexFunctionExpression(node), node.ResultUsage(), err
	}
	attribute, node, err := CatalogKey(parent, key)
	if err != nil {
		return "", false, sqltype.Usage{}, err
	}
	if node == nil {
		usage, err := KeyBaseUsage(parent, catalog.IndexKey{Attribute: attribute})
		if err != nil {
			return "", false, sqltype.Usage{}, err
		}
		text, err := (stored.Deparser{Output: presentation.Output}).Attribute(parent, attribute)
		return text, false, usage, err
	}
	text, err := (stored.Deparser{View: view, Relation: parent, Pretty: presentation.Pretty, Path: presentation.Path, Output: presentation.Output}).SQL(ctx, node, stored.DeparseParent{})
	if err != nil {
		return "", false, sqltype.Usage{}, err
	}
	return text, !PGIndexFunctionExpression(node), node.ResultUsage(), nil
}
