package access

import (
	"context"
	"fmt"
	"sort"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// OwnedRoots lists the ownership roots owned by sources, ordered by object ID
// then class ID.
func OwnedRoots(catalog catalog.Read, sources map[authorization.PrincipalRef]bool, operatorSeeded func(*catalog.Operator) bool) []ids.ObjectAddress {
	var roots []ids.ObjectAddress
	for a, d := range catalog.Descriptors() {
		owner, _, _ := DescriptorACL(d)
		if sources[owner] && OwnershipRoot(catalog, d, operatorSeeded) {
			roots = append(roots, a)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].ObjectID != roots[j].ObjectID {
			return roots[i].ObjectID < roots[j].ObjectID
		}
		return roots[i].ClassID < roots[j].ClassID
	})
	return roots
}

// OwnershipRelationLocks lists the relations to lock for roots. Ownership
// locks follow canonical associations, not generated names. Include
// independent roots and their backing relations, indexes and owned sequences.
// Non-nil policySources additionally locks containing relations for actual policy
// deletions, without expanding those surviving relations' automatic companions.
func OwnershipRelationLocks(view catalog.Read, roots []ids.ObjectAddress, policySources map[authorization.PrincipalRef]bool) []ids.ObjectAddress {
	group := make(map[ids.ObjectAddress]bool, len(roots))
	for _, a := range roots {
		switch d := view.Get(a).(type) {
		case *catalog.Relation:
			group[a] = true
		case *catalog.Type:
			if d.OwningRelation != (ids.ObjectAddress{}) {
				group[d.OwningRelation] = true
			}
		}
	}
	for a, d := range view.Descriptors() {
		r, ok := d.(*catalog.Relation)
		if !ok {
			continue
		}
		if r.SQLIndex != nil && group[r.SQLIndex.Parent] {
			group[a] = true
		}
		if r.Sequence != nil {
			parent := r.Sequence.Owned
			parent.SubID = 0
			if group[parent] {
				group[a] = true
			}
		}
	}
	if len(policySources) != 0 {
		for a, d := range view.Descriptors() {
			r, ok := d.(*catalog.Relation)
			if !ok || group[a] {
				continue
			}
			for _, policy := range r.Security.Policies {
				deleted := len(policy.Roles) != 0
				for _, target := range policy.Roles {
					if target.Public || !policySources[target.Ref] {
						deleted = false
						break
					}
				}
				if deleted {
					group[a] = true
					break
				}
			}
		}
	}
	locks := make([]ids.ObjectAddress, 0, len(group))
	for a := range group {
		locks = append(locks, a)
	}
	sort.Slice(locks, func(i, j int) bool { return locks[i].ObjectID < locks[j].ObjectID })
	return locks
}

// OwnedMembershipCandidate returns the next role record without the memberships
// that sources granted, or nil when none needs removal.
func OwnedMembershipCandidate(ctx context.Context, roles authorization.Read, revision uint64, sources map[authorization.PrincipalRef]bool) (*RoleRecord, error) {
	if !authorization.OwnedMembershipNeeded(roles, sources) {
		return nil, ctx.Err()
	}
	if revision == ^uint64(0) {
		return nil, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	root, err := authorization.OwnedMembershipCandidate(ctx, roles, sources)
	if err != nil {
		return nil, err
	}
	return &RoleRecord{Root: root, Revision: revision + 1}, nil
}

// OwnedPolicies drops sources from policy role lists, removing policies left
// without roles. It reports whether anything changed and never mutates input.
func OwnedPolicies(policies []catalog.PolicyDefinition, sources map[authorization.PrincipalRef]bool) ([]catalog.PolicyDefinition, bool) {
	needed := false
	for _, policy := range policies {
		for _, role := range policy.Roles {
			needed = needed || !role.Public && sources[role.Ref]
		}
	}
	if !needed {
		return policies, false
	}
	out := make([]catalog.PolicyDefinition, 0, len(policies))
	for _, policy := range policies {
		removed := 0
		for _, role := range policy.Roles {
			if !role.Public && sources[role.Ref] {
				removed++
			}
		}
		if removed == 0 {
			out = append(out, policy)
			continue
		}
		if removed == len(policy.Roles) {
			continue
		}
		copy := policy
		copy.Roles = make([]catalog.PolicyRole, 0, len(policy.Roles)-removed)
		for _, role := range policy.Roles {
			if role.Public || !sources[role.Ref] {
				copy.Roles = append(copy.Roles, role)
			}
		}
		out = append(out, copy)
	}
	return out, true
}
