package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// System lookup contributes one candidate per column-visible range item, after
// that item's ordinary names. Qualified lookup selects its nearest range item
// first and never searches a different frame on a missing member.
func (b *Binder) queryTableOID(namespace *QueryNamespace, qualified *QueryQualifier, ordinary *QueryColumn, frame uint32, pos parser.Position) (expr.Expr, error) {
	if b.Consumer.CheckConsumer || b.Target.GeneratedOwner != nil {
		return nil, nil
	}
	var candidate *QueryQualifier
	consider := func(q *QueryQualifier) error {
		if q.TableOIDOccurrence == 0 || qualified == nil && q.SystemColumnsHidden {
			return nil
		}
		for _, c := range q.Columns {
			if c.Descriptor.Name == "tableoid" {
				return nil
			}
		}
		if candidate != nil || ordinary != nil {
			return expr.ErrorAt("42702", `column reference "tableoid" is ambiguous`, pos)
		}
		candidate = q
		return nil
	}
	if qualified != nil {
		if err := consider(qualified); err != nil {
			return nil, err
		}
	} else if namespace != nil {
		for i := range namespace.Qualifiers {
			if err := consider(&namespace.Qualifiers[i]); err != nil {
				return nil, err
			}
		}
	}
	if candidate == nil {
		return nil, nil
	}
	if b.Clause.AggregateClause == "FROM" && b.Clause.stringFromForbidden {
		return nil, stringLateralError(*candidate, pos, false)
	}
	for i := len(b.Query.Scopes) - 1; i >= 0; i-- {
		if scope := &b.Query.Scopes[i]; scope.frame == frame && scope.stringFromForbidden {
			return nil, stringLateralError(*candidate, pos, false)
		}
	}
	index := candidate.TableOIDOccurrence - 1
	if index < 0 || index >= len(b.Query.Sources) {
		return nil, fmt.Errorf("stuntgres: invalid tableoid source occurrence")
	}
	leaf := &b.Query.Sources[index]
	if leaf.Ref != candidate.Ref || !leaf.Relation.HasSystemAttributes() {
		return nil, fmt.Errorf("stuntgres: invalid tableoid source identity")
	}
	leaf.TableOIDRead = true
	ref := leaf.Ref
	ref.Address.SubID = -6
	b.dependency(ref, leaf.Relation.Revision())
	return plan.TableOIDExpr{Info: b.Info(b.Type(sqltype.Oid).Usage(), pos, expr.Properties{RowDependent: true}), Owner: leaf.Ref, Scope: 1, Frame: frame, QueryOccurrence: candidate.TableOIDOccurrence}, nil
}

func queryNamespaceHasTableOID(namespace *QueryNamespace, occurrence int) bool {
	if namespace == nil || occurrence <= 0 {
		return false
	}
	for _, qualifier := range namespace.Qualifiers {
		if qualifier.TableOIDOccurrence == occurrence {
			return true
		}
	}
	return false
}
