package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// remapPreparedCatalog consumes an unpublished private candidate under the target
// install lock against captured authorities. No callbacks, source lock or external
// I/O run here. ReplaceCatalog already projected its immutable source into private
// maps, so principal remapping does not need another catalog clone.
func (e *Engine) remapPreparedCatalog(state *catalog.State, source, targetSnapshot authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) (*catalog.State, error) {
	e.retainDatabaseLocked(state)
	remap := e.replacementPrincipalMapper(source, targetSnapshot, expected)
	acl := func(a authorization.ACLValue) (authorization.ACLValue, error) {
		if len(a.Entries) == 0 {
			return a, nil
		}
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
		if _, err := e.roleRead(e.catalog.read(e.catalog.state), e.roles).Descriptor(target); err != nil {
			return authorization.PrincipalRef{}, err
		}
		return target, nil
	}
	for addr, d := range state.Descriptors() {
		switch d := d.(type) {
		case *catalog.Database, *catalog.TSTemplate:
			// Attachment authority is local; text search templates have no owner.
		case *catalog.Extension:
			n := *d
			var err error
			n.Owner, err = remap(d.Owner)
			if err != nil {
				return nil, err
			}
			state.SetDescriptor(addr, &n)
		case *catalog.ForeignObject:
			n := *d
			var err error
			if d.Owner != (authorization.PrincipalRef{}) {
				n.Owner, err = remap(d.Owner)
				if err != nil {
					return nil, err
				}
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return nil, err
			}
			if d.MappedRole != (authorization.PrincipalRef{}) {
				n.MappedRole, err = remap(d.MappedRole)
				if err != nil {
					return nil, err
				}
			}
			if d.Address().ClassID == ids.ForeignMappingClassID {
				n.ObjectName = catalog.ForeignMappingName(n.Server, n.MappedRole)
				state.UnbindName(catalog.DescriptorNameKey(d))
				state.BindName(catalog.DescriptorNameKey(&n), n.Address())
			}
			state.SetDescriptor(addr, &n)
		case *catalog.Procedure:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, d.Seeded() || d.Foreign != nil)
			if err != nil {
				return nil, err
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return nil, err
			}
			state.SetDescriptor(addr, &n)
		case *catalog.Parameter:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, true)
			if err != nil {
				return nil, err
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return nil, err
			}
			installParameterGrant(state, &n)
		case *catalog.Tablespace:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, true)
			if err != nil {
				return nil, err
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return nil, err
			}
			state.SetDescriptor(addr, &n)
		case *catalog.TSDictionary:
			n := *d
			var err error
			n.Owner, err = remap(d.Owner)
			if err != nil {
				return nil, err
			}
			state.SetDescriptor(addr, &n)
		case *catalog.Type:
			n := *d
			var err error
			seeded := d.OwningRelation == (ids.ObjectAddress{}) && !d.SQLType.IsUserDefined() && !(d.SQLType.IsArray() && d.SQLType.Element().IsUserDefined())
			n.Owner, err = seedOwner(d.Owner, seeded)
			if err != nil {
				return nil, err
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return nil, err
			}
			state.SetDescriptor(addr, &n)
		case *catalog.Collation:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, d.Seeded())
			if err != nil {
				return nil, err
			}
			state.SetDescriptor(addr, &n)
		case *catalog.Conversion:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, d.Seeded())
			if err != nil {
				return nil, err
			}
			state.SetDescriptor(addr, &n)
		case *catalog.Operator:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, operatorSeeded(d))
			if err != nil {
				return nil, err
			}
			state.SetDescriptor(addr, &n)
		case *catalog.Namespace:
			n := *d
			var err error
			n.Owner, err = remap(d.Owner)
			if err != nil {
				return nil, err
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return nil, err
			}
			state.SetDescriptor(addr, &n)
		case *catalog.Relation:
			n := *d
			var err error
			n.Owner, err = remap(d.Owner)
			if err != nil {
				return nil, err
			}
			n.Security.ACL, err = acl(d.Security.ACL)
			if err != nil {
				return nil, err
			}
			if len(d.Security.ColumnACL) != 0 {
				n.Security.ColumnACL = make(map[int32]authorization.ACLValue, len(d.Security.ColumnACL))
				for sub, a := range d.Security.ColumnACL {
					n.Security.ColumnACL[sub], err = acl(a)
					if err != nil {
						return nil, err
					}
				}
			}
			if len(d.Security.Policies) != 0 {
				n.Security.Policies = append([]catalog.PolicyDefinition(nil), d.Security.Policies...)
				for i := range n.Security.Policies {
					n.Security.Policies[i].Roles = append([]catalog.PolicyRole(nil), d.Security.Policies[i].Roles...)
					for j := range n.Security.Policies[i].Roles {
						role := &n.Security.Policies[i].Roles[j]
						if role.Public {
							continue
						}
						role.Ref, err = remap(role.Ref)
						if err != nil {
							return nil, err
						}
					}
				}
			}
			state.SetDescriptor(addr, &n)
		default:
			return nil, fmt.Errorf("stuntgres: catalog provider has no principal remapping contract")
		}
	}
	if state.DefaultACLCount() != 0 {
		next := make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord, state.DefaultACLCount())
		for key, record := range state.DefaultACLs() {
			creator, err := remap(key.Creator)
			if err != nil {
				return nil, err
			}
			value, err := acl(record.ACL)
			if err != nil {
				return nil, err
			}
			key.Creator = creator
			next[key] = &catalog.DefaultACLRecord{ACL: value}
		}
		state.SetDefaultACLs(next)
	}
	if err := catalog.ValidateDefaultACLs(e.roleRead(e.catalog.read(state), e.roles), e.catalog.read(state)); err != nil {
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
		if _, err := e.roleRead(e.catalog.read(e.catalog.state), e.roles).Descriptor(target.Ref); err != nil {
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
	database := e.catalog.state.MustDatabase(catalog.CurrentDatabaseAddress)
	e.mu.Unlock()
	stage := NewWithSettings(nil, e.databaseName, false)
	if err := stage.InstallForeignDefinitions(e.foreignWrappers, e.foreignValidators, e.foreignValueImporter, e.foreignValueExporter); err != nil {
		panic(err)
	}
	stage.importTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef, sourceRoles.root.Read(authorization.PrincipalRef{}).RoleCount())
	roles := sourceRoles.root.Reprovenance(&stage.authorizationDomain, stage.importTargets)
	retainedDatabase := *database
	retainedDatabase.Owner = stage.authorizationDomain.Principal(database.Owner.ID())
	retainedDatabase.ACL.Entries = append([]authorization.ACLEntry(nil), database.ACL.Entries...)
	for i := range retainedDatabase.ACL.Entries {
		entry := &retainedDatabase.ACL.Entries[i]
		entry.Grantor = stage.authorizationDomain.Principal(entry.Grantor.ID())
		if !entry.Grantee.Public {
			entry.Grantee.Role = stage.authorizationDomain.Principal(entry.Grantee.Role.ID())
		}
	}
	stage.catalog.state.SetDescriptor(catalog.CurrentDatabaseAddress, &retainedDatabase)
	stage.roles = &roleCatalog{root: roles, revision: sourceRoles.revision}
	stage.nextPrincipal = nextPrincipal
	return stage
}
