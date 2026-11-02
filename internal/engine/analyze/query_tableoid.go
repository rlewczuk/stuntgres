package analyze

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// System lookup contributes one candidate per column-visible range item, after
// that item's ordinary names. Qualified lookup selects its nearest range item
// first and never searches a different frame on a missing member.
func (b *Binder) querySystemAttribute(name string, namespace *QueryNamespace, qualified *QueryQualifier, ordinary *QueryColumn, frame uint32, pos parser.Position) (expr.Expr, error) {
	if b.Consumer.CheckConsumer || b.Target.GeneratedOwner != nil {
		return nil, nil
	}
	subID, typeID := catalog.SystemAttribute(name)
	mergeWhen := b.Target.mergeWhenFrame != 0 && b.FrameID() == b.Target.mergeWhenFrame
	if subID == 0 && !(mergeWhen && catalog.IsSystemColumn(name)) {
		return nil, nil
	}
	var candidate *QueryQualifier
	consider := func(q *QueryQualifier) error {
		if q.SystemOccurrence == 0 || qualified == nil && q.SystemColumnsHidden {
			return nil
		}
		for _, c := range q.Columns {
			if c.Descriptor.Name == name {
				return nil
			}
		}
		if mergeWhen && name != "tableoid" {
			return expr.ErrorAt("42P10", fmt.Sprintf(`cannot use system column "%s" in MERGE WHEN condition`, name), pos)
		}
		if candidate != nil || ordinary != nil {
			return expr.ErrorAt("42702", fmt.Sprintf(`column reference "%s" is ambiguous`, name), pos)
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
	if candidate.SystemOccurrence == -1 {
		return b.targetSystemAttribute(name, frame, pos), nil
	}
	index := candidate.SystemOccurrence - 1
	if index < 0 || index >= len(b.Query.Sources) {
		return nil, fmt.Errorf("stuntgres: invalid system attribute source occurrence")
	}
	leaf := &b.Query.Sources[index]
	if leaf.Ref != candidate.Ref || !leaf.Relation.HasSystemAttributes() {
		return nil, fmt.Errorf("stuntgres: invalid system attribute source identity")
	}
	leaf.SystemAttributeRead = true
	ref := leaf.Ref
	ref.Address.SubID = subID
	b.dependency(ref, leaf.Relation.Revision())
	return plan.SystemAttributeExpr{SubID: subID, Info: b.Info(b.Type(typeID).Usage(), pos, expr.Properties{RowDependent: true}), Owner: leaf.Ref, Scope: 1, Frame: frame, QueryOccurrence: candidate.SystemOccurrence}, nil
}

// Mutation target frames have no source occurrence. Their version and physical
// relation are supplied by the writer, independently of any FROM/USING suffix.
func (b *Binder) targetSystemAttribute(name string, frame uint32, pos parser.Position) plan.SystemAttributeExpr {
	subID, typeID := catalog.SystemAttribute(name)
	ref := b.Target.Ref
	ref.Address.SubID = subID
	b.dependency(ref, b.Target.Relation.Revision())
	b.Target.SystemAttributeRead = true
	b.Target.returningReadRequired = true
	return plan.SystemAttributeExpr{Info: b.Info(b.Type(typeID).Usage(), pos, expr.Properties{RowDependent: true}), Owner: b.Target.Ref, SubID: subID, Scope: 1, Frame: frame}
}

func queryNamespaceHasSystemAttribute(namespace *QueryNamespace, occurrence int) bool {
	if namespace == nil || occurrence <= 0 {
		return false
	}
	for _, qualifier := range namespace.Qualifiers {
		if qualifier.SystemOccurrence == occurrence {
			return true
		}
	}
	return false
}
