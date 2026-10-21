package catalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// PrincipalRemap maps the principals a catalog state stores into another
// role domain. The engine builds the mappers; catalog code only applies them.
type PrincipalRemap struct {
	// Principal maps an ordinary owner, grantor, grantee or policy role.
	Principal func(authorization.PrincipalRef) (authorization.PrincipalRef, error)
	// SeededOwner maps an owner that may be a seeded object's bootstrap
	// principal; seeded says whether the descriptor is a seeded object.
	SeededOwner func(ref authorization.PrincipalRef, seeded bool) (authorization.PrincipalRef, error)
	// OperatorSeeded reports whether an operator is a seeded builtin.
	OperatorSeeded func(*Operator) bool
}

// RemapPrincipals rewrites every owner, ACL and policy role of an
// unpublished private candidate, then its default ACL records. Each changed
// descriptor is a copy; the first mapping error stops the rewrite. Callers
// validate the default ACLs against the target roles afterwards.
func RemapPrincipals(state *State, m PrincipalRemap) error {
	remap, seedOwner := m.Principal, m.SeededOwner
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
	for addr, d := range state.Descriptors() {
		switch d := d.(type) {
		case *Database, *TSTemplate:
			// Attachment authority is local; text search templates have no owner.
		case *Extension:
			n := *d
			var err error
			n.Owner, err = remap(d.Owner)
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *ForeignObject:
			n := *d
			var err error
			if d.Owner != (authorization.PrincipalRef{}) {
				n.Owner, err = remap(d.Owner)
				if err != nil {
					return err
				}
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return err
			}
			if d.MappedRole != (authorization.PrincipalRef{}) {
				n.MappedRole, err = remap(d.MappedRole)
				if err != nil {
					return err
				}
			}
			if d.Address().ClassID == ids.ForeignMappingClassID {
				n.ObjectName = ForeignMappingName(n.Server, n.MappedRole)
				state.UnbindName(DescriptorNameKey(d))
				state.BindName(DescriptorNameKey(&n), n.Address())
			}
			state.SetDescriptor(addr, &n)
		case *Procedure:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, d.Seeded() || d.Foreign != nil)
			if err != nil {
				return err
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *Parameter:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, true)
			if err != nil {
				return err
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return err
			}
			InstallParameterGrant(state, &n)
		case *Tablespace:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, true)
			if err != nil {
				return err
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *LargeObject:
			n := *d
			var err error
			n.Owner, err = remap(d.Owner)
			if err != nil {
				return err
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *TSDictionary:
			n := *d
			var err error
			n.Owner, err = remap(d.Owner)
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *Type:
			n := *d
			var err error
			seeded := d.OwningRelation == (ids.ObjectAddress{}) && !d.SQLType.IsUserDefined() && !(d.SQLType.IsArray() && d.SQLType.Element().IsUserDefined())
			n.Owner, err = seedOwner(d.Owner, seeded)
			if err != nil {
				return err
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *Collation:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, d.Seeded())
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *Conversion:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, d.Seeded())
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *Operator:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, m.OperatorSeeded(d))
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *Namespace:
			n := *d
			var err error
			n.Owner, err = remap(d.Owner)
			if err != nil {
				return err
			}
			n.ACL, err = acl(d.ACL)
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *Relation:
			n := *d
			var err error
			n.Owner, err = remap(d.Owner)
			if err != nil {
				return err
			}
			n.Security.ACL, err = acl(d.Security.ACL)
			if err != nil {
				return err
			}
			if len(d.Security.ColumnACL) != 0 {
				n.Security.ColumnACL = make(map[int32]authorization.ACLValue, len(d.Security.ColumnACL))
				for sub, a := range d.Security.ColumnACL {
					n.Security.ColumnACL[sub], err = acl(a)
					if err != nil {
						return err
					}
				}
			}
			if len(d.Security.Policies) != 0 {
				n.Security.Policies = append([]PolicyDefinition(nil), d.Security.Policies...)
				for i := range n.Security.Policies {
					n.Security.Policies[i].Roles = append([]PolicyRole(nil), d.Security.Policies[i].Roles...)
					for j := range n.Security.Policies[i].Roles {
						role := &n.Security.Policies[i].Roles[j]
						if role.Public {
							continue
						}
						role.Ref, err = remap(role.Ref)
						if err != nil {
							return err
						}
					}
				}
			}
			state.SetDescriptor(addr, &n)
		default:
			return fmt.Errorf("stuntgres: catalog provider has no principal remapping contract")
		}
	}
	if state.DefaultACLCount() != 0 {
		next := make(map[DefaultACLKey]*DefaultACLRecord, state.DefaultACLCount())
		for key, record := range state.DefaultACLs() {
			creator, err := remap(key.Creator)
			if err != nil {
				return err
			}
			value, err := acl(record.ACL)
			if err != nil {
				return err
			}
			key.Creator = creator
			next[key] = &DefaultACLRecord{OID: record.OID, ACL: value}
		}
		state.SetDefaultACLs(next)
	}
	return nil
}
