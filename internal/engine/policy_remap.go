package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func RemapPolicies(view catalog.Read, d *catalog.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation typedef.CollationRemapper) (*catalog.Relation, error) {
	if len(d.Security.Policies) == 0 {
		return d, nil
	}
	copy := *d
	copy.Security.Policies = append([]catalog.PolicyDefinition(nil), d.Security.Policies...)
	for i := range copy.Security.Policies {
		p := &copy.Security.Policies[i]
		for _, slot := range []*catalog.PolicyPayload{&p.Using, &p.Check} {
			if *slot == nil {
				continue
			}
			expression := stored.PolicyOf(*slot)
			frozen, err := stored.RemapView(view, expression.Presentation(), mapping, ref, typ, collation)
			if err != nil {
				return nil, err
			}
			*slot = stored.NewPolicy(expression.Syntax(), frozen.Query().Root.(*plan.ProjectNode).Expressions[0], frozen)
		}
	}
	return &copy, nil
}
