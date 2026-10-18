package engine

import (
	"context"
	"fmt"
	"iter"
	"maps"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func policyDepends(p policyDefinition, target ids.ObjectAddress) bool {
	for _, expr := range []*policyExpression{p.using, p.check} {
		if expr == nil {
			continue
		}
		for _, leaf := range expr.presentation.sources {
			if target.SubID == 0 && leaf.ref.Address == target {
				return true
			}
		}
		for _, dep := range expr.presentation.dependencies {
			a := dep.ref.Address
			if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
				return true
			}
		}
	}
	return false
}
func policyDependentLabels(catalog CatalogRead, targets []ids.ObjectAddress, path []ids.ObjectAddress) []supportedDependent {
	return policyDependentLabelsFrom(catalog, targets, path, maps.All(catalog.state.descriptors))
}

func policyDependentLabelsFrom(catalog CatalogRead, targets []ids.ObjectAddress, path []ids.ObjectAddress, descriptors iter.Seq2[ids.ObjectAddress, Descriptor]) []supportedDependent {
	var dependents []supportedDependent
	for _, descriptor := range descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if slices.Contains(targets, d.address) {
			continue
		}
		for _, p := range d.policies {
			for _, target := range targets {
				if policyDepends(p, target) {
					dependents = append(dependents, supportedDependent{oid: p.OID, target: target, label: "policy " + dependencyIdentifier(p.Name) + " on table " + dependencyRelationName(catalog, d, path)})
					break
				}
			}
		}
	}
	return dependents
}
func stagePolicyDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode) error {
	return stageDropProvider(context.Background(), stage, nil, nil, nil).policyDependents(targets, mode)
}

func (provider *dropProvider) policyDependents(targets []ids.ObjectAddress, mode parser.DropMode) error {
	if mode == parser.DropRestrict {
		return nil
	}
	for address, descriptor := range provider.inputDescriptors() {
		if provider.removed(address) || slices.Contains(targets, address) {
			continue
		}
		if replacement := provider.replacement(address); replacement != nil {
			descriptor = replacement
		}
		d, ok := descriptor.(*relationDescriptor)
		if !ok || len(d.policies) == 0 {
			continue
		}
		var kept []policyDefinition
		for _, p := range d.policies {
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
		if len(kept) == len(d.policies) {
			continue
		}
		copy := *d
		copy.policies = kept
		revision, err := provider.revision(copy.revision)
		if err != nil {
			return err
		}
		copy.revision = revision
		if err = provider.replace(&copy); err != nil {
			return err
		}
	}
	return nil
}
func policyTypeChangeError(catalog CatalogRead, target ids.ObjectAddress) error {
	var owners []*relationDescriptor
	for _, descriptor := range catalog.state.descriptors {
		if d, ok := descriptor.(*relationDescriptor); ok {
			owners = append(owners, d)
		}
	}
	slices.SortFunc(owners, func(a, b *relationDescriptor) int {
		if a.address.ObjectID < b.address.ObjectID {
			return -1
		}
		if a.address.ObjectID > b.address.ObjectID {
			return 1
		}
		return 0
	})
	for _, owner := range owners {
		for _, p := range owner.policies {
			if !policyDepends(p, target) {
				continue
			}
			provider := catalog.state.descriptors[ids.ObjectAddress{ClassID: target.ClassID, ObjectID: target.ObjectID}].(*relationDescriptor)
			column := ""
			for i, a := range provider.attributes {
				if a == target.SubID {
					column = provider.columns[i].Name
					break
				}
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot alter type of a column used in a policy definition", Detail: fmt.Sprintf(`policy %s on table %s depends on column "%s"`, dependencyIdentifier(p.Name), dependencyIdentifier(owner.name), column)}
		}
	}
	return nil
}
