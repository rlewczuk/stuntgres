package engine

import "stuntgres/internal/sqltype"

func remapPolicies(view CatalogRead, d *relationDescriptor, mapping map[ObjectAddress]ObjectAddress, ref func(ObjectRef) ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation collationRemapper) (*relationDescriptor, error) {
	if len(d.policies) == 0 {
		return d, nil
	}
	copy := *d
	copy.policies = append([]policyDefinition(nil), d.policies...)
	for i := range copy.policies {
		p := &copy.policies[i]
		for _, slot := range []**policyExpression{&p.using, &p.check} {
			if *slot == nil {
				continue
			}
			expression := **slot
			frozen, err := remapFrozenPresentation(view, expression.presentation, mapping, ref, typ, collation)
			if err != nil {
				return nil, err
			}
			expression.presentation = frozen
			expression.expression = frozen.query.Root.(*projectNode).expressions[0]
			*slot = &expression
		}
	}
	return &copy, nil
}
