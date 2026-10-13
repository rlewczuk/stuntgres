package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
)

// remapPreparedCatalog consumes an unpublished private candidate under the target
// install lock against captured authorities. No callbacks, source lock or external
// I/O run here. ReplaceCatalog already projected its immutable source into private
// maps, so principal remapping does not need another catalog clone.
func (e *Engine) remapPreparedCatalog(state *catalogState, source, targetSnapshot authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) (*catalogState, error) {
	e.retainDatabaseLocked(state)
	remap := e.replacementPrincipalMapper(source, targetSnapshot, expected)
	acl := func(a authorization.ACLValue) (authorization.ACLValue, error) {
		n := authorization.ACLValue{Explicit: a.Explicit, Entries: make([]authorization.ACLEntry, len(a.Entries))}
		for i, x := range a.Entries {
			var err error
			x.Grantor, err = remap(x.Grantor)
			if err != nil {
				return authorization.ACLValue{}, err
			}
			if !x.Grantee.Public {
				x.Grantee.Role, err = remap(x.Grantee.Role)
				if err != nil {
					return authorization.ACLValue{}, err
				}
			}
			n.Entries[i] = x
		}
		return n, nil
	}
	seedOwner := func(ref authorization.PrincipalRef, seeded bool) (authorization.PrincipalRef, error) {
		if !seeded || ref.ID() != authorization.BootstrapPrincipalID {
			return remap(ref)
		}
		if _, err := source.Descriptor(ref); err != nil {
			return authorization.PrincipalRef{}, err
		}
		target := e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)
		if _, err := targetSnapshot.Descriptor(target); err != nil {
			return authorization.PrincipalRef{}, err
		}
		if _, err := e.roleRead(CatalogRead{e, e.state}, e.roles).Descriptor(target); err != nil {
			return authorization.PrincipalRef{}, err
		}
		return target, nil
	}
	for addr, d := range state.descriptors {
		switch d := d.(type) {
		case *databaseDescriptor, *tsTemplateDescriptor:
			// Retained attachment authority is already local; templates have no owner.
		case *extensionDescriptor:
			n := *d
			var err error
			n.owner, err = remap(d.owner)
			if err != nil {
				return nil, err
			}
			state.descriptors[addr] = &n
		case *procedureDescriptor:
			n := *d
			var err error
			n.owner, err = seedOwner(d.owner, d.conversion.Name() != "")
			if err != nil {
				return nil, err
			}
			n.acl, err = acl(d.acl)
			if err != nil {
				return nil, err
			}
			state.descriptors[addr] = &n
		case *tsDictionaryDescriptor:
			n := *d
			var err error
			n.owner, err = remap(d.owner)
			if err != nil {
				return nil, err
			}
			state.descriptors[addr] = &n
		case *typeDescriptor:
			if d.owningRelation == (ObjectAddress{}) && !d.typ.IsUserDefined() && !(d.typ.IsArray() && d.typ.Element().IsUserDefined()) {
				continue
			}
			n := *d
			var err error
			n.owner, err = remap(d.owner)
			if err != nil {
				return nil, err
			}
			n.acl, err = acl(d.acl)
			if err != nil {
				return nil, err
			}
			state.descriptors[addr] = &n
		case *collationDescriptor:
			n := *d
			var err error
			n.owner, err = seedOwner(d.owner, d.seeded())
			if err != nil {
				return nil, err
			}
			state.descriptors[addr] = &n
		case *conversionDescriptor:
			n := *d
			var err error
			n.owner, err = seedOwner(d.owner, d.seeded())
			if err != nil {
				return nil, err
			}
			state.descriptors[addr] = &n
		case *namespaceDescriptor:
			n := *d
			var err error
			n.owner, err = remap(d.owner)
			if err != nil {
				return nil, err
			}
			n.acl, err = acl(d.acl)
			if err != nil {
				return nil, err
			}
			state.descriptors[addr] = &n
		case *relationDescriptor:
			n := *d
			var err error
			n.owner, err = remap(d.owner)
			if err != nil {
				return nil, err
			}
			n.acl, err = acl(d.acl)
			if err != nil {
				return nil, err
			}
			if len(d.columnACL) != 0 {
				n.columnACL = make(map[int32]authorization.ACLValue, len(d.columnACL))
				for sub, a := range d.columnACL {
					n.columnACL[sub], err = acl(a)
					if err != nil {
						return nil, err
					}
				}
			}
			if len(d.policies) != 0 {
				n.policies = append([]policyDefinition(nil), d.policies...)
				for i := range n.policies {
					n.policies[i].roles = append([]policyRole(nil), d.policies[i].roles...)
					for j := range n.policies[i].roles {
						role := &n.policies[i].roles[j]
						if role.public {
							continue
						}
						role.ref, err = remap(role.ref)
						if err != nil {
							return nil, err
						}
					}
				}
			}
			state.descriptors[addr] = &n
		default:
			return nil, fmt.Errorf("stuntgres: catalog provider has no principal remapping contract")
		}
	}
	if len(state.defaultACLs) != 0 {
		next := make(map[defaultACLKey]*defaultACLRecord, len(state.defaultACLs))
		for key, record := range state.defaultACLs {
			creator, err := remap(key.creator)
			if err != nil {
				return nil, err
			}
			value, err := acl(record.acl)
			if err != nil {
				return nil, err
			}
			key.creator = creator
			next[key] = &defaultACLRecord{acl: value}
		}
		state.defaultACLs = next
	}
	if err := validateDefaultACLs(e.roleRead(CatalogRead{e, state}, e.roles), CatalogRead{e, state}); err != nil {
		return nil, err
	}
	return state, nil
}

// Both authorization ACLs and typed ACL data use the same captured name mapping
// and target-incarnation fences. Each replacement owns its private memoization.
func (e *Engine) replacementPrincipalMapper(source, targetSnapshot authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) func(authorization.PrincipalRef) (authorization.PrincipalRef, error) {
	mapping := make(map[authorization.PrincipalRef]authorization.PrincipalRef)
	return func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if target, ok := mapping[ref]; ok {
			return target, nil
		}
		d, err := source.Descriptor(ref)
		if err != nil {
			return authorization.PrincipalRef{}, err
		}
		target, ok := targetSnapshot.Lookup(d.Name)
		if !ok {
			return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q does not exist", d.Name)
		}
		if _, err := e.roleRead(CatalogRead{e, e.state}, e.roles).Descriptor(target.Ref); err != nil {
			return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q changed", d.Name)
		}
		if expected != nil && expected[ref] != target.Ref {
			return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q changed", d.Name)
		}
		mapping[ref] = target.Ref
		return target.Ref, nil
	}
}

func (e *Engine) restoreStage() *Engine {
	// Capture before reading: dropping and recreating an owner while a reader
	// is blocked cannot silently adopt the replacement role with the same name.
	e.mu.Lock()
	sourceRoles, nextPrincipal := e.roles, e.nextPrincipal
	database := e.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	e.mu.Unlock()
	stage := NewWithSettings(nil, e.databaseName)
	stage.importTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef, sourceRoles.root.Read(authorization.PrincipalRef{}).RoleCount())
	roles := sourceRoles.root.Reprovenance(&stage.authorizationDomain, stage.importTargets)
	retainedDatabase := *database
	retainedDatabase.owner = stage.authorizationDomain.Principal(database.owner.ID())
	retainedDatabase.acl.Entries = append([]authorization.ACLEntry(nil), database.acl.Entries...)
	for i := range retainedDatabase.acl.Entries {
		entry := &retainedDatabase.acl.Entries[i]
		entry.Grantor = stage.authorizationDomain.Principal(entry.Grantor.ID())
		if !entry.Grantee.Public {
			entry.Grantee.Role = stage.authorizationDomain.Principal(entry.Grantee.Role.ID())
		}
	}
	stage.state.descriptors[currentDatabaseAddress] = &retainedDatabase
	stage.roles = &roleCatalog{root: roles, revision: sourceRoles.revision}
	stage.nextPrincipal = nextPrincipal
	return stage
}
