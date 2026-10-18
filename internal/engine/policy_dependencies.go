package engine

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func policyDepends(p catalog.PolicyDefinition, target ids.ObjectAddress) bool {
	for _, condition := range []catalog.PolicyPayload{p.Using, p.Check} {
		if condition != nil && condition.DependsOn(target) {
			return true
		}
	}
	return false
}
func policyDependentLabels(catalog CatalogRead, targets []ids.ObjectAddress, path []ids.ObjectAddress) []supportedDependent {
	return policyDependentLabelsFrom(catalog, targets, path, catalog.Descriptors())
}

func policyDependentLabelsFrom(view CatalogRead, targets []ids.ObjectAddress, path []ids.ObjectAddress, descriptors iter.Seq2[ids.ObjectAddress, Descriptor]) []supportedDependent {
	var dependents []supportedDependent
	for _, descriptor := range descriptors {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if slices.Contains(targets, d.Address()) {
			continue
		}
		for _, p := range d.Policies {
			for _, target := range targets {
				if policyDepends(p, target) {
					dependents = append(dependents, supportedDependent{oid: p.OID, target: target, label: "policy " + dependencyIdentifier(p.Name) + " on table " + dependencyRelationName(view, d, path)})
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
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(d.Policies) == 0 {
			continue
		}
		var kept []catalog.PolicyDefinition
		for _, p := range d.Policies {
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
		if len(kept) == len(d.Policies) {
			continue
		}
		copy := *d
		copy.Policies = kept
		revision, err := provider.revision(copy.Revision())
		if err != nil {
			return err
		}
		copy.ObjectRevision = revision
		if err = provider.replace(&copy); err != nil {
			return err
		}
	}
	return nil
}
func policyTypeChangeError(view CatalogRead, target ids.ObjectAddress) error {
	var owners []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		if d, ok := descriptor.(*catalog.Relation); ok {
			owners = append(owners, d)
		}
	}
	slices.SortFunc(owners, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	for _, owner := range owners {
		for _, p := range owner.Policies {
			if !policyDepends(p, target) {
				continue
			}
			provider := view.MustRelation(ids.ObjectAddress{ClassID: target.ClassID, ObjectID: target.ObjectID})
			column := ""
			for i, a := range provider.Attributes {
				if a == target.SubID {
					column = provider.Columns[i].Name
					break
				}
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot alter type of a column used in a policy definition", Detail: fmt.Sprintf(`policy %s on table %s depends on column "%s"`, dependencyIdentifier(p.Name), dependencyIdentifier(owner.Name()), column)}
		}
	}
	return nil
}
