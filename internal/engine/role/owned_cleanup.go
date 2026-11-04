package role

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// prepareOwnedCleanup is the single ACL/policy/default/initial-privilege kernel.
// Its explicit input prefix is metadata only; every read is from the real stage.
func prepareOwnedCleanup(ctx context.Context, stage OwnedCleanupStage, roles authorization.Read, sources map[authorization.PrincipalRef]bool, plan *OwnedCleanupPlan) error {
	for address, descriptor := range stage.BaseCatalog().Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		recordOwnedACLCommands(plan, descriptor, sources)
		if plan.doomed(address) {
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
			plan.effects = append(plan.effects, ownedCleanupEffect{descriptor: &next})
		case *catalog.Namespace:
			next := *old
			next.ACL = nextACL
			next.ObjectRevision = revision
			plan.effects = append(plan.effects, ownedCleanupEffect{descriptor: &next})
		case *catalog.Type:
			next := *old
			next.ACL = nextACL
			next.ACLSyntheticDefault = false
			next.ObjectRevision = revision
			plan.effects = append(plan.effects, ownedCleanupEffect{descriptor: &next})
		case *catalog.Procedure:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			plan.effects = append(plan.effects, ownedCleanupEffect{descriptor: &next})
		case *catalog.Language:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			plan.effects = append(plan.effects, ownedCleanupEffect{descriptor: &next})
		case *catalog.ForeignObject:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			plan.effects = append(plan.effects, ownedCleanupEffect{descriptor: &next})
		case *catalog.Parameter:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			if !next.ACL.Explicit || authorization.MatchesDefaultBaseline(next.ACL, next.Owner, authorization.DefaultParameters, false) {
				plan.effects = append(plan.effects, ownedCleanupEffect{remove: stage.BaseRef(address)})
			} else {
				plan.effects = append(plan.effects, ownedCleanupEffect{descriptor: &next})
			}
		case *catalog.Tablespace:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			plan.effects = append(plan.effects, ownedCleanupEffect{descriptor: &next})
		case *catalog.LargeObject:
			next := *old
			next.ACL, next.ObjectRevision = nextACL, revision
			plan.effects = append(plan.effects, ownedCleanupEffect{descriptor: &next})
		case *catalog.Relation:
			next := *old
			next.Security.ACL = nextACL
			next.ObjectRevision = revision
			next.Security.Policies = policies
			if nextColumns != nil {
				next.Security.ColumnACL = nextColumns
			}
			plan.effects = append(plan.effects, ownedCleanupEffect{descriptor: &next})
		default:
			return fmt.Errorf("stuntgres: object has no ACL cleanup handler")
		}
	}
	for key, original := range stage.BaseCatalog().State().DefaultACLs() {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := original
		if plan.namespaceRemoved(key.Schema) {
			continue
		}
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
		plan.effects = append(plan.effects, ownedCleanupEffect{setDefault: true, defaultKey: key, defaultRecord: next})
	}
	// Filter authentic earlier private initial-privilege inputs by exactly the
	// canonical raw prefix and cleanup's own real parameter removals.
	current := plan.source
	for address, record := range current.State().InitialPrivileges() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if plan.doomed(address) {
			continue
		}
		removed := false
		for _, effect := range plan.effects {
			if effect.remove != (ids.ObjectRef{}) && catalog.InitialPrivilegeRemoved(address, effect.remove.Address) {
				removed = true
				break
			}
		}
		if removed {
			continue
		}
		object := address
		object.SubID = 0
		owner, _, _ := access.DescriptorACL(current.Get(object))
		acl, changed, err := cleanupInitialACL(ctx, roles, record.ACL, owner, sources)
		if err != nil {
			return err
		}
		if changed {
			plan.effects = append(plan.effects, ownedCleanupEffect{initial: address, privType: record.PrivType, acl: acl, owner: owner})
		}
	}
	return nil
}
