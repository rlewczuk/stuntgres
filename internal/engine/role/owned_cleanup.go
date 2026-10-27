package role

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// StageOwnedCleanup stages DROP OWNED's cleanup for sources in stage: their
// memberships (joined to the stage's publication), ACL entries, policy roles
// and default privileges. roles and publication are the captured role state.
func StageOwnedCleanup(ctx context.Context, stage OwnedCleanupStage, roles authorization.Read, publication *access.RoleRecord, sources map[authorization.PrincipalRef]bool) error {
	candidate, err := access.OwnedMembershipCandidate(ctx, roles, publication, sources)
	if err != nil {
		return err
	}
	roles = stage.SetRoleCandidate(candidate, publication)
	for address, descriptor := range stage.BaseCatalog().Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, removed := stage.Removal(address); removed {
			continue
		}
		if replacement := stage.Get(address); replacement != nil {
			descriptor = replacement
		}
		owner, acl, columns := access.DescriptorACL(descriptor)
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
			policies, different = access.OwnedPolicies(relation.Security.Policies, sources)
			changed = changed || different
		}
		if !changed {
			continue
		}
		revision, err := stage.CascadeRevision(descriptor.Revision())
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
		case *catalog.Language:
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
				err = stage.Remove(stage.BaseRef(address))
			} else {
				err = stage.Replace(&next)
			}
		case *catalog.Tablespace:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			err = stage.Replace(&next)
		case *catalog.LargeObject:
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
	for key, original := range stage.BaseCatalog().State().DefaultACLs() {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := original
		if staged, exists := stage.DefaultACLChange(key); exists {
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
				next = &catalog.DefaultACLRecord{OID: record.OID, ACL: acl}
			}
		}
		// Creator ownership removes the entire actual record, including a
		// stored empty global policy. Recipient cleanup uses the key baseline.
		stage.ObserveDefault(key)
		stage.SetDefaultACL(key, next)
	}
	return nil
}

// Only singular versus plural matters to the DROP OWNED primary. A real
// source-grantor membership deletion or wholly selected policy supplies another
// collected object, even when its enclosing relation is already an owner root.
// ACL row edits are not deletion roots and automatic parent associations do not
// contribute an invented object count.
func OwnedCleanupAddsDeletionRoot(stage OwnedCleanupStage, sources map[authorization.PrincipalRef]bool) bool {
	if stage.RoleCandidate() != nil {
		return true
	}
	// Each actual creator-owned policy is independently collected, including
	// scoped records already tombstoned by namespace AUTO removal. Recipient
	// normalization and other creators' AUTO records are not owner roots.
	for key := range stage.BaseCatalog().State().DefaultACLs() {
		if sources[key.Creator] {
			return true
		}
	}
	for address, descriptor := range stage.Descriptors() {
		next, ok := descriptor.(*catalog.Relation)
		original, existed := stage.BaseCatalog().Relation(address)
		if ok && existed && len(next.Security.Policies) < len(original.Security.Policies) {
			return true
		}
	}
	// Column-owned sequences have their own real shared owner dependency.
	// They are excluded from independent mutation roots, not from collection.
	for _, descriptor := range stage.BaseCatalog().Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if ok && relation.RelationKind == catalog.SequenceRelation && relation.Sequence != nil &&
			relation.Sequence.Owned != (ids.ObjectAddress{}) && sources[relation.Owner] {
			return true
		}
	}
	// Cleanup skips already removed relations; their selected policies are
	// nevertheless separate collected addresses in PostgreSQL.
	for address := range stage.Removals() {
		relation, ok := stage.BaseCatalog().Relation(address)
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
