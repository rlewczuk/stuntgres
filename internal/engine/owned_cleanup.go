package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
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
	return &roleCatalog{Root: root, Revision: revision + 1}, nil
}
func ownedPolicies(policies []catalog.PolicyDefinition, sources map[authorization.PrincipalRef]bool) ([]catalog.PolicyDefinition, bool) {
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
func stageOwnedCleanup(ctx context.Context, stage *CatalogStage, roles authorization.Read, publication *roleCatalog, sources map[authorization.PrincipalRef]bool) error {
	candidate, err := ownedMembershipCandidate(ctx, roles, publication.Revision, sources)
	if err != nil {
		return err
	}
	stage.roleCandidate = candidate
	roles = stage.roleRead(publication, stage.owner.stageRead(stage))
	for address, descriptor := range stage.Base.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, removed := stage.Removals[address]; removed {
			continue
		}
		if replacement := stage.Descriptors[address]; replacement != nil {
			descriptor = replacement
		}
		owner, acl, columns := DescriptorACL(descriptor)
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
		var policies []catalog.PolicyDefinition
		if relation, ok := descriptor.(*catalog.Relation); ok {
			var different bool
			policies, different = ownedPolicies(relation.Security.Policies, sources)
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
		case *catalog.Database:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			err = stage.Replace(&next)
		case *catalog.Namespace:
			next := *old
			next.ACL = nextACL
			next.ObjectRevision = revision
			err = stage.Replace(&next)
		case *catalog.Type:
			next := *old
			next.ACL = nextACL
			next.ACLSyntheticDefault = false
			next.ObjectRevision = revision
			err = stage.Replace(&next)
		case *catalog.Procedure:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			err = stage.Replace(&next)
		case *catalog.ForeignObject:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			err = stage.Replace(&next)
		case *catalog.Parameter:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			if !next.ACL.Explicit || authorization.MatchesDefaultBaseline(next.ACL, next.Owner, authorization.DefaultParameters, false) {
				err = stage.Remove(stage.owner.catalog.Domain.Ref(stage.Base.Epoch(), address))
			} else {
				err = stage.Replace(&next)
			}
		case *catalog.Tablespace:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			err = stage.Replace(&next)
		case *catalog.Relation:
			next := *old
			next.Security.ACL = nextACL
			next.ObjectRevision = revision
			next.Security.Policies = policies
			if nextColumns != nil {
				next.Security.ColumnACL = nextColumns
			}
			err = stage.Replace(&next)
		default:
			return fmt.Errorf("stuntgres: object has no ACL cleanup handler")
		}
		if err != nil {
			return err
		}
	}
	for key, original := range stage.Base.State().DefaultACLs() {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := original
		if staged, exists := stage.DefaultACLs[key]; exists {
			if staged == nil {
				continue // Namespace AUTO removal must not resurrect this key.
			}
			record = staged
		}
		var next *catalog.DefaultACLRecord
		if !sources[key.Creator] {
			acl, changed, err := authorization.OwnedACL(ctx, roles, record.ACL, key.Creator, sources)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			if !authorization.MatchesDefaultBaseline(acl, key.Creator, key.Class, key.Schema != (ids.ObjectAddress{})) {
				next = &catalog.DefaultACLRecord{ACL: acl}
			}
		}
		// Creator ownership removes the entire actual record, including a
		// stored empty global policy. Recipient cleanup uses the key baseline.
		if stage.DefaultReads == nil {
			stage.DefaultReads = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
		}
		if stage.DefaultACLs == nil {
			stage.DefaultACLs = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
		}
		stage.DefaultReads[key] = original
		stage.DefaultACLs[key] = next
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
	for key := range stage.Base.State().DefaultACLs() {
		if sources[key.Creator] {
			return true
		}
	}
	for address, descriptor := range stage.Descriptors {
		next, ok := descriptor.(*catalog.Relation)
		original, existed := stage.Base.Relation(address)
		if ok && existed && len(next.Security.Policies) < len(original.Security.Policies) {
			return true
		}
	}
	// Column-owned sequences have their own real shared owner dependency.
	// They are excluded from independent mutation roots, not from collection.
	for _, descriptor := range stage.Base.Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if ok && relation.RelationKind == catalog.SequenceRelation && relation.Sequence != nil &&
			relation.Sequence.Owned != (ids.ObjectAddress{}) && sources[relation.Owner] {
			return true
		}
	}
	// Cleanup skips already removed relations; their selected policies are
	// nevertheless separate collected addresses in PostgreSQL.
	for address := range stage.Removals {
		relation, ok := stage.Base.Relation(address)
		if !ok {
			continue
		}
		for _, policy := range relation.Security.Policies {
			selected := len(policy.Roles) > 0
			for _, role := range policy.Roles {
				if role.Public || !sources[role.Ref] {
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
	if current.Revision != s.roleRevision {
		return fmt.Errorf("stuntgres: authorization changed before publication")
	}
	if current.Revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	next := s.roleCandidate
	if next.Revision != current.Revision+1 {
		return fmt.Errorf("stuntgres: invalid ownership role candidate")
	}
	return current.Root.ValidateDeletionCandidate(&next.Root)
}
func (s *CatalogStage) roleRead(current *roleCatalog, catalog catalog.Read) authorization.Read {
	if s.roleCandidate != nil {
		return s.owner.roles.read(s.owner.catalog.owner(), catalog, s.roleCandidate)
	}
	return s.owner.roles.read(s.owner.catalog.owner(), catalog, current)
}
