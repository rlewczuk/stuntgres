package engine

import (
	"context"
	"fmt"
	"slices"
)

func ownedMembershipCandidate(ctx context.Context, roles RoleRead, sources map[PrincipalRef]bool) (*roleCatalog, error) {
	needed := false
	for _, edge := range roles.state.edges {
		if sources[edge.Grantor] {
			needed = true
			break
		}
	}
	if !needed {
		return nil, ctx.Err()
	}
	if roles.state.revision == ^uint64(0) {
		return nil, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	actions := make([]membershipRevokeAction, len(roles.state.edges))
	for i, edge := range roles.state.edges {
		if sources[edge.Grantor] {
			if err := planMembershipRevoke(ctx, roles.state.edges, actions, edge.Role, i, false, true); err != nil {
				return nil, err
			}
		}
	}
	edges := make([]Membership, 0, len(roles.state.edges))
	for i, edge := range roles.state.edges {
		if actions[i] != membershipRevokeDelete {
			edges = append(edges, edge)
		}
	}
	next := advanceRoleRevision(roles.state)
	next.edges = edges
	return next, nil
}

// ALL/CASCADE revokes every selected grantee and grantor provenance using the
// ordinary selective ACL planner, preserving independently supported grants.
func ownedACL(ctx context.Context, roles RoleRead, acl ACLValue, owner PrincipalRef, sources map[PrincipalRef]bool) (ACLValue, bool, error) {
	next := acl
	changed := false
	for _, entry := range acl.Entries {
		if err := ctx.Err(); err != nil {
			return acl, false, err
		}
		// Retire received rights first. Recursive revocation can still find
		// inherited owner or independently inherited grant-option support.
		if entry.Grantee.Public || !sources[entry.Grantee.Role] {
			continue
		}
		candidate, different, err := changeObjectACL(ctx, roles, next, owner, entry.Grantor, []ACLGrantee{entry.Grantee}, entry.Privileges|entry.GrantOptions, false, true, false, true)
		if err != nil {
			return acl, false, err
		}
		next = candidate
		changed = changed || different
	}
	for _, entry := range next.Entries {
		if err := ctx.Err(); err != nil {
			return acl, false, err
		}
		if !sources[entry.Grantor] || !entry.Grantee.Public && sources[entry.Grantee.Role] {
			continue
		}
		mask := entry.Privileges | entry.GrantOptions
		supported, err := scopedACLGrantOptions(ctx, roles, next, owner, entry.Grantor, mask)
		if err != nil {
			return acl, false, err
		}
		mask &^= supported
		if mask == 0 {
			continue
		}
		candidate, different, err := changeObjectACL(ctx, roles, next, owner, entry.Grantor, []ACLGrantee{entry.Grantee}, mask, false, true, false, true)
		if err != nil {
			return acl, false, err
		}
		next = candidate
		changed = changed || different
	}
	return next, changed, nil
}
func ownedPolicies(policies []policyDefinition, sources map[PrincipalRef]bool) ([]policyDefinition, bool) {
	needed := false
	for _, policy := range policies {
		for _, role := range policy.roles {
			needed = needed || !role.public && sources[role.ref]
		}
	}
	if !needed {
		return policies, false
	}
	out := make([]policyDefinition, 0, len(policies))
	for _, policy := range policies {
		removed := 0
		for _, role := range policy.roles {
			if !role.public && sources[role.ref] {
				removed++
			}
		}
		if removed == 0 {
			out = append(out, policy)
			continue
		}
		if removed == len(policy.roles) {
			continue
		}
		copy := policy
		copy.roles = make([]policyRole, 0, len(policy.roles)-removed)
		for _, role := range policy.roles {
			if role.public || !sources[role.ref] {
				copy.roles = append(copy.roles, role)
			}
		}
		out = append(out, copy)
	}
	return out, true
}
func stageOwnedCleanup(ctx context.Context, stage *CatalogStage, roles RoleRead, sources map[PrincipalRef]bool) error {
	candidate, err := ownedMembershipCandidate(ctx, roles, sources)
	if err != nil {
		return err
	}
	stage.roleCandidate = candidate
	roles = stage.roleRead(roles.state, stage.owner.stageRead(stage))
	for address, descriptor := range stage.base.state.descriptors {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, removed := stage.removals[address]; removed {
			continue
		}
		if replacement := stage.descriptors[address]; replacement != nil {
			descriptor = replacement
		}
		owner, acl, columns := descriptorACL(descriptor)
		nextACL, changed, err := ownedACL(ctx, roles, acl, owner, sources)
		if err != nil {
			return err
		}
		var nextColumns map[int32]ACLValue
		for sub, column := range columns {
			next, different, err := ownedACL(ctx, roles, column, owner, sources)
			if err != nil {
				return err
			}
			if different {
				if nextColumns == nil {
					nextColumns = make(map[int32]ACLValue, len(columns))
					for key, value := range columns {
						nextColumns[key] = value
					}
				}
				nextColumns[sub] = next
				changed = true
			}
		}
		var policies []policyDefinition
		if relation, ok := descriptor.(*relationDescriptor); ok {
			var different bool
			policies, different = ownedPolicies(relation.policies, sources)
			changed = changed || different
		}
		if !changed {
			continue
		}
		revision, err := stage.cascadeMetadataRevision(descriptor.Revision())
		if err != nil {
			return err
		}
		switch old := descriptor.(type) {
		case *databaseDescriptor:
			next := *old
			next.acl, next.revision = nextACL, revision
			err = stage.Replace(&next)
		case *namespaceDescriptor:
			next := *old
			next.acl = nextACL
			next.revision = revision
			err = stage.Replace(&next)
		case *typeDescriptor:
			next := *old
			next.acl = nextACL
			next.revision = revision
			err = stage.Replace(&next)
		case *relationDescriptor:
			next := *old
			next.acl = nextACL
			next.revision = revision
			next.policies = policies
			if nextColumns != nil {
				next.columnACL = nextColumns
			}
			err = stage.Replace(&next)
		default:
			return fmt.Errorf("stuntgres: object has no ACL cleanup handler")
		}
		if err != nil {
			return err
		}
	}
	for key, original := range stage.base.state.defaultACLs {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := original
		if staged, exists := stage.defaultACLs[key]; exists {
			if staged == nil {
				continue // Namespace AUTO removal must not resurrect this key.
			}
			record = staged
		}
		var next *defaultACLRecord
		if !sources[key.creator] {
			acl, changed, err := ownedACL(ctx, roles, record.acl, key.creator, sources)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			if !matchesDefaultBaseline(acl, key.creator, key.class, key.schema != (ObjectAddress{})) {
				next = &defaultACLRecord{acl: acl}
			}
		}
		// Creator ownership removes the entire actual record, including a
		// stored empty global policy. Recipient cleanup uses the key baseline.
		if stage.defaultReads == nil {
			stage.defaultReads = make(map[defaultACLKey]*defaultACLRecord)
		}
		if stage.defaultACLs == nil {
			stage.defaultACLs = make(map[defaultACLKey]*defaultACLRecord)
		}
		stage.defaultReads[key] = original
		stage.defaultACLs[key] = next
	}
	return nil
}

// Only singular versus plural matters to the DROP OWNED primary. A real
// source-grantor membership deletion or wholly selected policy supplies another
// collected object, even when its enclosing relation is already an owner root.
// ACL row edits are not deletion roots and automatic parent associations do not
// contribute an invented object count.
func ownedCleanupAddsDeletionRoot(stage *CatalogStage, sources map[PrincipalRef]bool) bool {
	if stage.roleCandidate != nil {
		return true
	}
	// Each actual creator-owned policy is independently collected, including
	// scoped records already tombstoned by namespace AUTO removal. Recipient
	// normalization and other creators' AUTO records are not owner roots.
	for key := range stage.base.state.defaultACLs {
		if sources[key.creator] {
			return true
		}
	}
	for address, descriptor := range stage.descriptors {
		next, ok := descriptor.(*relationDescriptor)
		original, existed := stage.base.state.descriptors[address].(*relationDescriptor)
		if ok && existed && len(next.policies) < len(original.policies) {
			return true
		}
	}
	// Column-owned sequences have their own real shared owner dependency.
	// They are excluded from independent mutation roots, not from collection.
	for _, descriptor := range stage.base.state.descriptors {
		relation, ok := descriptor.(*relationDescriptor)
		if ok && relation.relationKind == sequenceRelation && relation.sequence != nil &&
			relation.sequence.owned != (ObjectAddress{}) && sources[relation.owner] {
			return true
		}
	}
	// Cleanup skips already removed relations; their selected policies are
	// nevertheless separate collected addresses in PostgreSQL.
	for address := range stage.removals {
		relation, ok := stage.base.state.descriptors[address].(*relationDescriptor)
		if !ok {
			continue
		}
		for _, policy := range relation.policies {
			selected := len(policy.roles) > 0
			for _, role := range policy.roles {
				if role.public || !sources[role.ref] {
					selected = false
					break
				}
			}
			if selected {
				return true
			}
		}
	}
	return false
}

// The optional candidate is membership deletion only. It shares immutable role
// descriptors/name maps and preserves the order and flags of surviving rows.
func (s *CatalogStage) validateRoleCandidate(current *roleCatalog) error {
	if s.roleCandidate == nil {
		return nil
	}
	if current.revision != s.roleRevision {
		return fmt.Errorf("stuntgres: authorization changed before publication")
	}
	if current.revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	next := s.roleCandidate
	if next.revision != current.revision+1 || len(next.roles) != len(current.roles) || len(next.names) != len(current.names) {
		return fmt.Errorf("stuntgres: invalid ownership role candidate")
	}
	for ref, role := range current.roles {
		if next.roles[ref] != role {
			return fmt.Errorf("stuntgres: invalid ownership role descriptors")
		}
	}
	for name, ref := range current.names {
		if next.names[name] != ref {
			return fmt.Errorf("stuntgres: invalid ownership role names")
		}
	}
	index := 0
	for _, edge := range next.edges {
		for index < len(current.edges) && current.edges[index] != edge {
			index++
		}
		if index == len(current.edges) {
			return fmt.Errorf("stuntgres: invalid ownership membership candidate")
		}
		index++
	}
	if slices.Equal(next.edges, current.edges) {
		return fmt.Errorf("stuntgres: unchanged ownership role candidate")
	}
	return nil
}
func (s *CatalogStage) roleRead(current *roleCatalog, catalog CatalogRead) RoleRead {
	if s.roleCandidate != nil {
		return RoleRead{s.owner, s.roleCandidate, catalog}
	}
	return RoleRead{s.owner, current, catalog}
}
