package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (provider *dropProvider) policyDependents(targets []ids.ObjectAddress, mode parser.DropMode) error {
	if mode == parser.DropRestrict {
		return nil
	}
	for address, descriptor := range provider.InputDescriptors() {
		if provider.Removed(address) || slices.Contains(targets, address) {
			continue
		}
		if replacement := provider.Replacement(address); replacement != nil {
			descriptor = replacement
		}
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(d.Security.Policies) == 0 {
			continue
		}
		var kept []catalog.PolicyDefinition
		for _, p := range d.Security.Policies {
			drop := false
			for _, target := range targets {
				if policyDepends(p, target) {
					drop = true
					break
				}
			}
			if !drop {
				kept = append(kept, p)
			}
		}
		if len(kept) == len(d.Security.Policies) {
			continue
		}
		copy := *d
		copy.Security.Policies = kept
		revision, err := provider.Revision(copy.Revision())
		if err != nil {
			return err
		}
		copy.ObjectRevision = revision
		if err = provider.Replace(&copy); err != nil {
			return err
		}
	}
	return nil
}
