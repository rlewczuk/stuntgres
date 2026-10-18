package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
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

func (e *Engine) preflightACLItemDump(ctx context.Context, catalog CatalogRead, output capturedACLItemOutput) error {
	var failure error
	check := func(value sqltype.Value) {
		if failure == nil {
			failure = preflightACLItemValue(ctx, output, value)
		}
	}
	expression := func(node expr.Expr) {
		walkBoundExpression(node, func(child expr.Expr) {
			if literal, ok := child.(literalExpression); ok {
				check(literal.value)
			}
		})
	}
	for _, descriptor := range catalog.Descriptors() {
		relation, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if ns, ok := catalog.Namespace(relation.namespace); ok && ns.temporary != (ids.SessionToken{}) {
			continue
		}
		if relation.RelationKind == catalogRelation {
			continue
		}
		if storage := e.storage[relation.address]; storage != nil {
			for _, version := range storage.versions {
				if !liveVersion(version, e.commitSeq, storage.ends, nil) {
					continue
				}
				for i, value := range version.values {
					if _, generated := relation.Generations[relation.Attributes[i]]; generated {
						continue
					}
					check(value)
				}
			}
		}
		for _, definition := range relation.Defaults {
			expression(definition.expression)
		}
		for _, definition := range relation.Generations {
			expression(definition.expression)
		}
		for _, constraint := range relation.CheckConstraints {
			expression(constraint.expression)
		}
		if relation.View != nil {
			for _, literal := range viewOf(relation).literals {
				check(literal.value)
			}
		}
		if relation.SQLIndex != nil {
			for _, key := range relation.SQLIndex.keys {
				if key.derived != nil {
					expression(key.derived.definition)
				}
			}
			if relation.SQLIndex.predicate != nil {
				expression(relation.SQLIndex.predicate.definition)
			}
		}
		if relation.Partition != nil {
			for _, key := range relation.Partition.keys {
				if key.derived != nil {
					expression(key.derived.definition)
				}
			}
		}
		if relation.Membership != nil {
			for _, datum := range relation.Membership.lower {
				check(datum.value)
			}
			for _, datum := range relation.Membership.upper {
				check(datum.value)
			}
		}
		for _, policy := range relation.Policies {
			if policy.using != nil {
				expression(policyExpressionOf(policy.using).expression)
			}
			if policy.check != nil {
				expression(policyExpressionOf(policy.check).expression)
			}
		}
		if failure != nil {
			return failure
		}
	}
	return ctx.Err()
}
