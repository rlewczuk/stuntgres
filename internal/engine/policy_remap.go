package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func remapPolicies(view CatalogRead, d *relationDescriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation collationRemapper) (*relationDescriptor, error) {
	if len(d.Policies) == 0 {
		return d, nil
	}
	copy := *d
	copy.Policies = append([]policyDefinition(nil), d.Policies...)
	for i := range copy.Policies {
		p := &copy.Policies[i]
		for _, slot := range []*policyPayload{&p.using, &p.check} {
			if *slot == nil {
				continue
			}
			expression := *policyExpressionOf(*slot)
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
