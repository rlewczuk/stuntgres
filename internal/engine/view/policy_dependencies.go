package view

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func PolicyDepends(p catalog.PolicyDefinition, target ids.ObjectAddress) bool {
	for _, condition := range []catalog.PolicyPayload{p.Using, p.Check} {
		if condition != nil && condition.DependsOn(target) {
			return true
		}
	}
	return false
}

func PolicyTypeChangeError(view catalog.Read, target ids.ObjectAddress) error {
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
			if !PolicyDepends(p, target) {
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
