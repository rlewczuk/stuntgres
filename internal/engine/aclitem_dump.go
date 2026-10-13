package engine

import (
	"context"

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
	expression := func(node BoundExpr) {
		walkBoundExpression(node, func(child BoundExpr) {
			if literal, ok := child.(literalExpression); ok {
				check(literal.value)
			}
		})
	}
	for _, descriptor := range catalog.state.descriptors {
		relation, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if ns, ok := catalog.state.descriptors[relation.namespace].(*namespaceDescriptor); ok && ns.temporary != (SessionToken{}) {
			continue
		}
		if relation.relationKind == catalogRelation {
			continue
		}
		if storage := e.storage[relation.address]; storage != nil {
			for _, version := range storage.versions {
				if !liveVersion(version, e.commitSeq, storage.ends, nil) {
					continue
				}
				for i, value := range version.values {
					if _, generated := relation.generations[relation.attributes[i]]; generated {
						continue
					}
					check(value)
				}
			}
		}
		for _, definition := range relation.defaults {
			expression(definition.expression)
		}
		for _, definition := range relation.generations {
			expression(definition.expression)
		}
		for _, constraint := range relation.checkConstraints {
			expression(constraint.expression)
		}
		if relation.view != nil {
			for _, literal := range relation.view.literals {
				check(literal.value)
			}
		}
		if relation.sqlIndex != nil {
			for _, key := range relation.sqlIndex.keys {
				if key.derived != nil {
					expression(key.derived.definition)
				}
			}
			if relation.sqlIndex.predicate != nil {
				expression(relation.sqlIndex.predicate.definition)
			}
		}
		if relation.partition != nil {
			for _, key := range relation.partition.keys {
				if key.derived != nil {
					expression(key.derived.definition)
				}
			}
		}
		if relation.membership != nil {
			for _, datum := range relation.membership.lower {
				check(datum.value)
			}
			for _, datum := range relation.membership.upper {
				check(datum.value)
			}
		}
		for _, policy := range relation.policies {
			if policy.using != nil {
				expression(policy.using.expression)
			}
			if policy.check != nil {
				expression(policy.check.expression)
			}
		}
		if failure != nil {
			return failure
		}
	}
	return ctx.Err()
}
