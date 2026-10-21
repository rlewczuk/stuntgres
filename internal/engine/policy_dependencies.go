package engine

import (
	"fmt"
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
func policyTypeChangeError(view catalog.Read, target ids.ObjectAddress) error {
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
		for _, p := range owner.Security.Policies {
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
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot alter type of a column used in a policy definition", Detail: fmt.Sprintf(`policy %s on table %s depends on column "%s"`, catalog.DependencyIdentifier(p.Name), catalog.DependencyIdentifier(owner.Name()), column)}
		}
	}
	return nil
}
