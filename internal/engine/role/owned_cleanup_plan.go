package role

import (
	"context"
	"iter"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
)

type ownedCleanupEffect struct {
	descriptor    catalog.Descriptor
	remove        ids.ObjectRef
	defaultKey    catalog.DefaultACLKey
	defaultRecord *catalog.DefaultACLRecord
	setDefault    bool
	initial       ids.ObjectAddress
	privType      byte
	owner         authorization.PrincipalRef
	acl           authorization.ACLValue
	command       string
}

// OwnedCleanupPlan contains only ordered genuine cleanup products. Source is a
// real coherent precleanup read, never a synthetic post-removal catalog.

// PrepareOwnedCleanup validates ordinary role/ACL work before any raw removal.
func PrepareOwnedCleanup(ctx context.Context, stage OwnedCleanupStage, roles authorization.Read, publication *access.RoleRecord, sources map[authorization.PrincipalRef]bool, raw *table.DropPreparation) (*OwnedCleanupPlan, error) {
	candidate, err := access.OwnedMembershipCandidate(ctx, roles, publication, sources)
	if err != nil {
		return nil, err
	}
	plan := &OwnedCleanupPlan{stage: stage, source: stage.StageRead(), candidate: candidate, publication: publication, raw: raw, addsDeletionRoot: candidate != nil}
	roles = stage.ReadRoleCandidate(candidate, publication)
	for key := range stage.BaseCatalog().State().DefaultACLs() {
		if sources[key.Creator] {
			plan.addsDeletionRoot = true
		}
	}
	for _, descriptor := range plan.source.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok {
			if relation.RelationKind == catalog.SequenceRelation && relation.Sequence != nil && relation.Sequence.Owned != (ids.ObjectAddress{}) && sources[relation.Owner] {
				plan.addsDeletionRoot = true
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
					plan.addsDeletionRoot = true
					plan.deletionRoots = append(plan.deletionRoots, ids.ObjectAddress{ClassID: ids.PolicyClassID, ObjectID: policy.OID})
				}
			}
		}
	}
	if err := prepareOwnedCleanup(ctx, stage, roles, sources, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

type OwnedCleanupPlan struct {
	stage                  OwnedCleanupStage
	source                 catalog.Read
	candidate, publication *access.RoleRecord
	effects                []ownedCleanupEffect
	raw                    *table.DropPreparation
	consumed               bool
	addsDeletionRoot       bool
	deletionRoots          []ids.ObjectAddress
}

func (p *OwnedCleanupPlan) CollectsDDLCommands() bool {
	collector, ok := p.stage.(interface{ CollectsDDLCommands() bool })
	return ok && collector.CollectsDDLCommands()
}
func (p *OwnedCleanupPlan) RecordDDLPrivileges(_ string, kind string) {
	p.effects = append(p.effects, ownedCleanupEffect{command: kind})
}
func (p *OwnedCleanupPlan) doomed(address ids.ObjectAddress) bool {
	if _, removed := p.stage.Removal(address); removed {
		return true
	}
	if p.raw == nil {
		return false
	}
	for _, effect := range p.raw.Effects() {
		if effect.Remove.Address != (ids.ObjectAddress{}) && catalog.InitialPrivilegeRemoved(address, effect.Remove.Address) {
			return true
		}
		if effect.Before != nil && address.ClassID == ids.RelationClassID && address.ObjectID == effect.Before.Address().ObjectID && address.SubID != 0 && !effect.After.HasAttribute(address.SubID) {
			return true
		}
		if next, ok := effect.Descriptor.(*catalog.Relation); ok && address.ClassID == ids.RelationClassID && address.ObjectID == next.Address().ObjectID && address.SubID != 0 && !next.HasAttribute(address.SubID) {
			return true
		}
	}
	return false
}
func (p *OwnedCleanupPlan) namespaceRemoved(address ids.ObjectAddress) bool {
	if p.raw != nil {
		for _, effect := range p.raw.Effects() {
			if effect.SchemaDefaults == address && address != (ids.ObjectAddress{}) {
				return true
			}
		}
	}
	return false
}
func (p *OwnedCleanupPlan) AddsDeletionRoot() bool             { return p.addsDeletionRoot }
func (p *OwnedCleanupPlan) DeletionRoots() []ids.ObjectAddress { return p.deletionRoots }
func (p *OwnedCleanupPlan) Effects() iter.Seq[table.DropEffect] {
	return func(yield func(table.DropEffect) bool) {
		for _, effect := range p.effects {
			if effect.descriptor != nil || effect.remove != (ids.ObjectRef{}) {
				if !yield(table.DropEffect{Descriptor: effect.descriptor, Remove: effect.remove}) {
					return
				}
			}
		}
	}
}
func (p *OwnedCleanupPlan) Apply(ctx context.Context, stage OwnedCleanupStage) error {
	if p == nil || p.stage != stage || p.consumed {
		return ids.ErrCatalogReplaced
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.consumed = true
	stage.SetRoleCandidate(p.candidate, p.publication)
	current := stage.StageRead()
	for _, effect := range p.effects {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch {
		case effect.command != "":
			if collector, ok := stage.(interface{ RecordDDLPrivileges(string, string) }); ok {
				collector.RecordDDLPrivileges("REVOKE", effect.command)
			}
		case effect.descriptor != nil:
			owner, _, _ := access.DescriptorACL(effect.descriptor)
			actual, _, _ := access.DescriptorACL(current.Get(effect.descriptor.Address()))
			if owner != actual {
				return ids.ErrCatalogReplaced
			}
			if err := stage.Replace(effect.descriptor); err != nil {
				return err
			}
		case effect.remove != (ids.ObjectRef{}):
			if err := stage.Remove(effect.remove); err != nil {
				return err
			}
		case effect.setDefault:
			stage.ObserveDefault(effect.defaultKey)
			stage.SetDefaultACL(effect.defaultKey, effect.defaultRecord)
		case effect.initial != (ids.ObjectAddress{}):
			object := effect.initial
			object.SubID = 0
			descriptor := current.Get(object)
			if descriptor == nil {
				return ids.ErrCatalogReplaced
			}
			owner, _, _ := access.DescriptorACL(descriptor)
			if owner != effect.owner {
				return ids.ErrCatalogReplaced
			}
			stage.RecordInitialPrivilege(effect.initial, effect.privType, effect.acl)
		}
	}
	return nil
}
