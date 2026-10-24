package authorization

import (
	"context"
	"fmt"
	"iter"
	"maps"
	"slices"
)

type membershipIdentity struct{ role, member, grantor PrincipalRef }

func membershipKey(edge Membership) membershipIdentity {
	return membershipIdentity{edge.Role, edge.Member, edge.Grantor}
}

// MergeRoots reapplies only the candidate's exact delta to the current root.
// All three inputs remain immutable; a touched predecessor must still match.
func MergeRoots(ctx context.Context, base, candidate, current *Root) (*Root, error) {
	if base == nil || candidate == nil || current == nil || base.domain != candidate.domain || base.domain != current.domain {
		return nil, fmt.Errorf("stuntgres: foreign role merge")
	}
	if current == base {
		return candidate, ctx.Err()
	}
	if candidate == base {
		return current, ctx.Err()
	}
	next := *current
	rolesChanged, namesChanged := false, false
	retired := false
	for ref, before := range base.roles {
		after, exists := candidate.roles[ref]
		if exists && after == before {
			continue
		}
		if actual, ok := current.roles[ref]; !ok || actual != before {
			return nil, fmt.Errorf("stuntgres: concurrent role descriptor change")
		}
		if !rolesChanged {
			next.roles = maps.Clone(current.roles)
			rolesChanged = true
		}
		retired = retired || !exists
		if !exists {
			delete(next.roles, ref)
		} else {
			next.roles[ref] = after
		}
	}
	for ref, after := range candidate.roles {
		if _, exists := base.roles[ref]; exists {
			continue
		}
		if _, exists := current.roles[ref]; exists {
			return nil, fmt.Errorf("stuntgres: concurrent principal insertion")
		}
		if !rolesChanged {
			next.roles = maps.Clone(current.roles)
			rolesChanged = true
		}
		next.roles[ref] = after
	}
	for name, before := range base.names {
		after, exists := candidate.names[name]
		if exists && after == before {
			continue
		}
		if actual, ok := current.names[name]; !ok || actual != before {
			return nil, fmt.Errorf("stuntgres: concurrent role name change")
		}
		if !namesChanged {
			next.names = maps.Clone(current.names)
			namesChanged = true
		}
		if !exists {
			delete(next.names, name)
		} else {
			next.names[name] = after
		}
	}
	for name, after := range candidate.names {
		if _, exists := base.names[name]; exists {
			continue
		}
		if _, exists := current.names[name]; exists {
			return nil, fmt.Errorf("stuntgres: concurrent role name insertion")
		}
		if !namesChanged {
			next.names = maps.Clone(current.names)
			namesChanged = true
		}
		next.names[name] = after
	}
	var changed map[membershipIdentity]bool
	if !slices.Equal(base.edges, candidate.edges) {
		beforeEdges := make(map[membershipIdentity]Membership, len(base.edges))
		afterEdges := make(map[membershipIdentity]Membership, len(candidate.edges))
		currentEdges := make(map[membershipIdentity]Membership, len(current.edges))
		for _, edge := range base.edges {
			beforeEdges[membershipKey(edge)] = edge
		}
		for _, edge := range candidate.edges {
			afterEdges[membershipKey(edge)] = edge
		}
		for _, edge := range current.edges {
			currentEdges[membershipKey(edge)] = edge
		}
		changed = make(map[membershipIdentity]bool)
		for key, before := range beforeEdges {
			after, exists := afterEdges[key]
			if exists && before == after {
				continue
			}
			if actual, ok := currentEdges[key]; !ok || actual != before {
				return nil, fmt.Errorf("stuntgres: concurrent role membership change")
			}
			changed[key] = true
		}
		for key := range afterEdges {
			if _, exists := beforeEdges[key]; exists {
				continue
			}
			if _, exists := currentEdges[key]; exists {
				return nil, fmt.Errorf("stuntgres: concurrent role membership insertion")
			}
			changed[key] = true
		}
		if len(changed) != 0 {
			next.edges = make([]Membership, 0, max(0, len(current.edges)+len(candidate.edges)-len(base.edges)))
			for _, edge := range current.edges {
				if !changed[membershipKey(edge)] {
					next.edges = append(next.edges, edge)
				}
			}
			for _, edge := range candidate.edges {
				if changed[membershipKey(edge)] {
					next.edges = append(next.edges, edge)
				}
			}
		}
	}
	if !rolesChanged && !namesChanged && len(changed) == 0 {
		return current, ctx.Err()
	}
	read := next.Read(PrincipalRef{})
	for _, edge := range next.edges {
		if !retired && !changed[membershipKey(edge)] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, ref := range [...]PrincipalRef{edge.Role, edge.Member, edge.Grantor} {
			if _, err := read.Descriptor(ref); err != nil {
				return nil, err
			}
		}
		if changed[membershipKey(edge)] {
			ancestors, err := read.RoleClosure(ctx, edge.Role, false)
			if err != nil {
				return nil, err
			}
			if slices.Contains(ancestors, edge.Member) {
				return nil, sqlError("0LP01", fmt.Sprintf("role \"%s\" is a member of role \"%s\"", next.roles[edge.Role].Name, next.roles[edge.Member].Name))
			}
		}
	}
	return &next, ctx.Err()
}

// AddedMembershipReferences yields exact principals of newly introduced edges;
// option-only updates and removals do not introduce shared dependencies.
func AddedMembershipReferences(base, candidate *Root) iter.Seq[PrincipalRef] {
	return func(yield func(PrincipalRef) bool) {
		for edge := range AddedMemberships(base, candidate) {
			for _, ref := range [...]PrincipalRef{edge.Role, edge.Member, edge.Grantor} {
				if !yield(ref) {
					return
				}
			}
		}
	}
}

func AddedMemberships(base, candidate *Root) iter.Seq[Membership] {
	return func(yield func(Membership) bool) {
		if slices.Equal(base.edges, candidate.edges) {
			return
		}
		prior := make(map[membershipIdentity]struct{}, len(base.edges))
		for _, edge := range base.edges {
			prior[membershipKey(edge)] = struct{}{}
		}
		for _, edge := range candidate.edges {
			if _, exists := prior[membershipKey(edge)]; !exists && !yield(edge) {
				return
			}
		}
	}
}
