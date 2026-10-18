package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func ownedMembershipCandidate(ctx context.Context, roles authorization.Read, revision uint64, sources map[authorization.PrincipalRef]bool) (*roleCatalog, error) {
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
	return &roleCatalog{root: root, revision: revision + 1}, nil
}
func ownedPolicies(policies []policyDefinition, sources map[authorization.PrincipalRef]bool) ([]policyDefinition, bool) {
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
func stageOwnedCleanup(ctx context.Context, stage *CatalogStage, roles authorization.Read, publication *roleCatalog, sources map[authorization.PrincipalRef]bool) error {
	candidate, err := ownedMembershipCandidate(ctx, roles, publication.revision, sources)
	if err != nil {
		return err
	}
	stage.roleCandidate = candidate
	roles = stage.roleRead(publication, stage.owner.stageRead(stage))
	for address, descriptor := range stage.base.Descriptors() {
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
		nextACL, changed, err := authorization.OwnedACL(ctx, roles, acl, owner, sources)
		if err != nil {
			return err
		}
		var nextColumns map[int32]authorization.ACLValue
		for sub, column := range columns {
			next, different, err := authorization.OwnedACL(ctx, roles, column, owner, sources)
			if err != nil {
				return err
			}
			if different {
				if nextColumns == nil {
					nextColumns = make(map[int32]authorization.ACLValue, len(columns))
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
			policies, different = ownedPolicies(relation.Policies, sources)
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
			next.ACL, next.revision = nextACL, revision
			err = stage.Replace(&next)
		case *namespaceDescriptor:
			next := *old
			next.ACL = nextACL
			next.revision = revision
			err = stage.Replace(&next)
		case *typeDescriptor:
			next := *old
			next.ACL = nextACL
			next.ACLSyntheticDefault = false
			next.revision = revision
			err = stage.Replace(&next)
		case *procedureDescriptor:
			next := *old
			next.ACL, next.revision = nextACL, revision
			err = stage.Replace(&next)
		case *foreignDescriptor:
			next := *old
			next.ACL, next.revision = nextACL, revision
			err = stage.Replace(&next)
		case *parameterDescriptor:
			next := *old
			next.ACL, next.revision = nextACL, revision
			if !next.ACL.Explicit || authorization.MatchesDefaultBaseline(next.ACL, next.Owner, authorization.DefaultParameters, false) {
				err = stage.Remove(stage.owner.catalog.domain.Ref(stage.base.Epoch(), address))
			} else {
				err = stage.Replace(&next)
			}
		case *tablespaceDescriptor:
			next := *old
			next.ACL, next.revision = nextACL, revision
			err = stage.Replace(&next)
		case *relationDescriptor:
			next := *old
			next.ACL = nextACL
			next.revision = revision
			next.Policies = policies
			if nextColumns != nil {
				next.ColumnACL = nextColumns
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
			acl, changed, err := authorization.OwnedACL(ctx, roles, record.acl, key.creator, sources)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			if !authorization.MatchesDefaultBaseline(acl, key.creator, key.class, key.schema != (ids.ObjectAddress{})) {
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
func ownedCleanupAddsDeletionRoot(stage *CatalogStage, sources map[authorization.PrincipalRef]bool) bool {
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
		original, existed := stage.base.Relation(address)
		if ok && existed && len(next.Policies) < len(original.Policies) {
			return true
		}
	}
	// Column-owned sequences have their own real shared owner dependency.
	// They are excluded from independent mutation roots, not from collection.
	for _, descriptor := range stage.base.Descriptors() {
		relation, ok := descriptor.(*relationDescriptor)
		if ok && relation.RelationKind == sequenceRelation && relation.Sequence != nil &&
			relation.Sequence.owned != (ids.ObjectAddress{}) && sources[relation.Owner] {
			return true
		}
	}
	// Cleanup skips already removed relations; their selected policies are
	// nevertheless separate collected addresses in PostgreSQL.
	for address := range stage.removals {
		relation, ok := stage.base.Relation(address)
		if !ok {
			continue
		}
		for _, policy := range relation.Policies {
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
	if next.revision != current.revision+1 {
		return fmt.Errorf("stuntgres: invalid ownership role candidate")
	}
	return current.root.ValidateDeletionCandidate(&next.root)
}
func (s *CatalogStage) roleRead(current *roleCatalog, catalog CatalogRead) authorization.Read {
	if s.roleCandidate != nil {
		return s.owner.roleRead(catalog, s.roleCandidate)
	}
	return s.owner.roleRead(catalog, current)
}
