package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Validate only ACL leaves; the regular serializers retain every other lossless
// boundary. This runs under the existing capture lock before any writer call.
func preflightACLItemValue(ctx context.Context, output capturedACLItemOutput, value sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if value.Type() == nil || value.IsNull() || !value.Type().ContainsACLItem() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := preflightACLItemValue(ctx, output, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		if value.Type().BaseType().ID() != sqltype.ACLItemArray && containsEmptyACL(value) {
			return fmt.Errorf("stuntgres: dump cannot serialize rank-one empty ACL inside an array of composite carriers")
		}
		for i := range value.ArrayLen() {
			if err := preflightACLItemValue(ctx, output, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	authority, grantee, grantor, _, _, err := value.ACLItem()
	if err != nil {
		return err
	}
	if grantee != 0 {
		if _, err := output.FormatACLRole(authority, grantee); err != nil {
			return err
		}
	}
	_, err = output.FormatACLRole(authority, grantor)
	return err
}

func (e *Engine) preflightACLItemDump(ctx context.Context, view catalog.Read, output capturedACLItemOutput) error {
	var failure error
	check := func(value sqltype.Value) {
		if failure == nil {
			failure = preflightACLItemValue(ctx, output, value)
		}
	}
	expression := func(node expr.Expr) {
		walkBoundExpression(node, func(child expr.Expr) {
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
		if storage := e.storage.Relations[relation.Address()]; storage != nil {
			for _, version := range storage.Versions {
				if !visibility.LiveVersion(version, e.visibility.CommitSeq(), storage.Ends, nil) {
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
			for _, literal := range viewOf(relation).literals {
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
