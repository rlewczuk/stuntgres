package engine

import "fmt"

// remapPreparedCatalog consumes an unpublished private candidate under the target
// install lock against captured authorities. No callbacks, source lock or external
// I/O run here. ReplaceCatalog already projected its immutable source into private
// maps, so principal remapping does not need another catalog clone.
func (e *Engine) remapPreparedCatalog(state *catalogState, source, targetSnapshot RoleRead, expected map[PrincipalRef]PrincipalRef) (*catalogState, error) {
	e.retainDatabaseLocked(state)
	mapping := make(map[PrincipalRef]PrincipalRef)
	remap := func(ref PrincipalRef) (PrincipalRef, error) {
		if target, ok := mapping[ref]; ok {
			return target, nil
		}
		d, err := source.Descriptor(ref)
		if err != nil {
			return PrincipalRef{}, err
		}
		target, ok := targetSnapshot.Lookup(d.Name)
		if !ok {
			return PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q does not exist", d.Name)
		}
		if _, err := (RoleRead{e, e.roles}).Descriptor(target.Ref); err != nil {
			return PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q changed", d.Name)
		}
		if expected != nil && expected[ref] != target.Ref {
			return PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q changed", d.Name)
		}
		mapping[ref] = target.Ref
		return target.Ref, nil
	}
	acl := func(a ACLValue) (ACLValue, error) {
		n := ACLValue{Explicit: a.Explicit, Entries: make([]ACLEntry, len(a.Entries))}
		for i, x := range a.Entries {
			var err error
			x.Grantor, err = remap(x.Grantor)
			if err != nil {
				return ACLValue{}, err
			}
			if !x.Grantee.Public {
				x.Grantee.Role, err = remap(x.Grantee.Role)
				if err != nil {
					return ACLValue{}, err
				}
			}
			n.Entries[i] = x
		}
		return n, nil
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
			n.owner, err = remap(d.owner)
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
			if d.seeded() && d.owner == (PrincipalRef{source.owner, 10}) {
				if _, err = source.Descriptor(d.owner); err != nil {
					return nil, err
				}
				n.owner = PrincipalRef{e, 10}
				if _, err = targetSnapshot.Descriptor(n.owner); err != nil {
					return nil, err
				}
				if _, err = (RoleRead{e, e.roles}).Descriptor(n.owner); err != nil {
					return nil, err
				}
			} else {
				n.owner, err = remap(d.owner)
			}
			if err != nil {
				return nil, err
			}
			state.descriptors[addr] = &n
		case *conversionDescriptor:
			// Immutable builtin identities have no principal references to remap.
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
				n.columnACL = make(map[int32]ACLValue, len(d.columnACL))
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
	return state, nil
}

func (e *Engine) restoreStage() *Engine {
	// Capture before reading: dropping and recreating an owner while a reader
	// is blocked cannot silently adopt the replacement role with the same name.
	e.mu.Lock()
	sourceRoles, nextPrincipal := e.roles, e.nextPrincipal
	e.mu.Unlock()
	stage := NewWithSettings(nil, e.databaseName)
	roles := &roleCatalog{revision: sourceRoles.revision, roles: make(map[PrincipalRef]RoleDescriptor, len(sourceRoles.roles)), names: make(map[string]PrincipalRef, len(sourceRoles.names))}
	stage.importTargets = make(map[PrincipalRef]PrincipalRef, len(sourceRoles.roles))
	for target, d := range sourceRoles.roles {
		ref := PrincipalRef{stage, target.id}
		d.Ref = ref
		roles.roles[ref] = d
		roles.names[d.Name] = ref
		stage.importTargets[ref] = target
	}
	stage.roles = roles
	stage.nextPrincipal = nextPrincipal
	return stage
}
