package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// preflightACLItemDump checks every ACL leaf a dump would write: domain
// defaults, live rows of persistent relations, stored expressions and bound
// literals. The caller holds Engine.mu and passes the captured relation roots
// and commit frontier.
func preflightACLItemDump(ctx context.Context, view catalog.Read, relations map[ids.ObjectAddress]*storage.Relation, frontier ids.CommitSeq, output regobj.ACLItemOutput) error {
	var failure error
	check := func(value sqltype.Value) {
		if failure == nil {
			failure = regobj.PreflightACLItemValue(ctx, output, value)
		}
	}
	expression := func(node expr.Expr) {
		plan.WalkExpression(node, func(child expr.Expr) {
			if literal, ok := child.(plan.LiteralExpr); ok {
				check(literal.Value)
			}
		})
	}
	for _, descriptor := range view.Descriptors() {
		if domain, ok := descriptor.(*catalog.Type); ok && domain.DomainDefault != nil {
			expression(domain.DomainDefault.Expression)
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if ns, ok := view.Namespace(relation.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) {
			continue
		}
		if relation.RelationKind == catalog.CatalogRelation {
			continue
		}
		if root := relations[relation.Address()]; root != nil {
			for _, version := range root.Versions {
				if !visibility.LiveVersion(version, frontier, root.Ends, nil) {
					continue
				}
				for i, value := range version.Values {
					if _, generated := relation.ColumnMeta.Generations[relation.Attributes[i]]; generated {
						continue
					}
					check(value)
				}
			}
		}
		for _, definition := range relation.ColumnMeta.Defaults {
			expression(definition.Expression)
		}
		for _, definition := range relation.ColumnMeta.Generations {
			expression(definition.Expression)
		}
		for _, constraint := range relation.Constraints.Check {
			expression(constraint.Expression)
		}
		if relation.View != nil {
			for _, literal := range viewOf(relation).Literals() {
				check(literal.Value)
			}
		}
		if relation.SQLIndex != nil {
			for _, key := range relation.SQLIndex.Keys {
				if key.Derived != nil {
					expression(key.Derived.Definition)
				}
			}
			if relation.SQLIndex.Predicate != nil {
				expression(relation.SQLIndex.Predicate.Definition)
			}
		}
		if relation.Partition != nil {
			for _, key := range relation.Partition.Keys {
				if key.Derived != nil {
					expression(key.Derived.Definition)
				}
			}
		}
		if relation.Membership != nil {
			for _, datum := range relation.Membership.Lower {
				check(datum.Value)
			}
			for _, datum := range relation.Membership.Upper {
				check(datum.Value)
			}
		}
		for _, policy := range relation.Security.Policies {
			if policy.Using != nil {
				expression(policyExpressionOf(policy.Using).expression)
			}
			if policy.Check != nil {
				expression(policyExpressionOf(policy.Check).expression)
			}
		}
		if failure != nil {
			return failure
		}
	}
	return ctx.Err()
}
