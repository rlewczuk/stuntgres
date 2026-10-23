package stored

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// Inference is a separate variable namespace: target Vars are unqualified,
// unlike the target and excluded namespaces in the update action below it.
func (a ruleRendering) conflictInferenceSQL(ctx context.Context, view catalog.Read, target *catalog.Relation) (string, error) {
	conflict := a.binding.Insert.Conflict
	a.inference = a.policy.Rule
	var sql strings.Builder
	if len(conflict.Keys) != 0 {
		parts := make([]string, len(conflict.Keys))
		for i, key := range conflict.Keys {
			var err error
			var input ids.OID
			if key.Attribute != 0 {
				parts[i], err = a.attribute(target, key.Attribute)
				for j, attribute := range target.Attributes {
					if attribute == key.Attribute {
						input = ids.OID(target.Columns[j].Usage.Type.ID())
						break
					}
				}
			} else {
				parts[i], err = ruleExpressionSQL(ctx, view, key.Expression, a)
				input = ids.OID(key.Expression.ResultUsage().Type.ID())
				_, call := a.syntax.(*parser.Insert).Conflict.Keys[i].Expression.(parser.FunctionExpr)
				if !a.policy.Rule || !call {
					parts[i] = "(" + parts[i] + ")"
				}
			}
			if err != nil {
				return "", err
			}
			if key.Collation != 0 {
				var name string
				if a.policy.Rule {
					name = collationDisplayName(view, key.Collation, a.policy.Path, a.policy.identifier)
					if name == "" {
						return "", fmt.Errorf("stuntgres: missing conflict collation")
					}
				} else {
					name = typedef.CollationSQLName(view, key.Collation)
				}
				parts[i] += " COLLATE " + name
			}
			if key.Class.OID() != 0 {
				class, exists := view.OperatorClass(ids.OID(key.Class.OID()))
				if !exists {
					return "", fmt.Errorf("stuntgres: missing conflict operator class")
				}
				if a.policy.Rule {
					if defaultClass, ok := view.DefaultOperatorClass(class.Method, input); ok && defaultClass.Address() == class.Address() {
						continue
					}
				}
				namespace, exists := view.Namespace(class.Namespace())
				if !exists {
					return "", fmt.Errorf("stuntgres: missing conflict operator class namespace")
				}
				name := a.policy.identifier(namespace.Name()) + "." + a.policy.identifier(class.Name())
				if a.policy.Rule {
					for _, ns := range a.policy.Path {
						if found, ok := view.LookupOperatorClass(ns, class.Method, class.Name()); ok {
							if found.Address() == class.Address() {
								name = a.policy.identifier(class.Name())
							}
							break
						}
					}
				}
				parts[i] += " " + name
			}
		}
		if !a.policy.Rule {
			sql.WriteByte(' ')
		}
		sql.WriteString("(" + strings.Join(parts, ", ") + ")")
	}
	if conflict.Predicate != nil {
		text, err := ruleExpressionSQL(ctx, view, conflict.Predicate, a)
		if err != nil {
			return "", err
		}
		sql.WriteString(a.clause(" WHERE ", 2, "WHERE ") + text)
	}
	return sql.String(), nil
}
