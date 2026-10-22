package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func (provider *dropProvider) ruleDependentLabels(targets []ids.ObjectAddress) []view.Dependent {
	if provider.prepared == nil {
		return view.RuleDependentLabels(provider.base, targets, provider.path)
	}
	return view.RuleDependentLabelsFrom(provider.base, targets, provider.path, provider.liveDescriptors(), provider.observe)
}

func (provider *dropProvider) ruleDependents(targets []ids.ObjectAddress, mode parser.DropMode) error {
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
		if !ok || len(stored.RulesOf(d)) == 0 {
			continue
		}
		kept := view.KeepRules(provider.base, stored.RulesOf(d), targets)
		if len(kept) == len(stored.RulesOf(d)) {
			continue
		}
		copy := *d
		copy.Rules = stored.RulesPayload(kept)
		revision, err := provider.Revision(copy.Revision())
		if err != nil {
			return err
		}
		copy.ObjectRevision = revision
		if err := provider.Replace(&copy); err != nil {
			return err
		}
	}
	return nil
}
