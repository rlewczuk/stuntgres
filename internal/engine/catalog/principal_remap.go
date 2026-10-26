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
	// Procedure shares the scoped original-declaration graph with stored queries.
	Procedure ProcedureRemapper
}

// ProcedureRemapper reprovenances an original immutable declaration, never a
// lookup of the current declaration at its address.
type ProcedureRemapper func(*Procedure) (*Procedure, error)

// NewProcedureRemapper retains sharing and cycles across all original carriers
// mapped with this principal policy.
func NewProcedureRemapper(m PrincipalRemap) ProcedureRemapper {
	_, procedure := principalMappings(m)
	return procedure
}

// RemapPrincipals rewrites every owner, ACL and policy role of an
// unpublished private candidate, then its default ACL records. Each changed
// descriptor is a copy; the first mapping error stops the rewrite. Callers
// validate the default ACLs against the target roles afterwards. It rewrites
// most of the catalog, so it runs as a bulk load.
func RemapPrincipals(state *State, m PrincipalRemap) error {
	state.BeginBulkLoad(0)
	defer state.EndBulkLoad()
	acl, procedure := principalMappings(m)
	if m.Procedure != nil {
		procedure = m.Procedure
	}
	remap, seedOwner := m.Principal, m.SeededOwner
	for addr, d := range state.Descriptors() {
		switch d := d.(type) {
		case *Database, *AccessMethod, *TSTemplate, *OperatorFamilyOperator, *OperatorFamilySupport, *Cast:
			// Attachment authority is local. Global access methods, templates,
			// family members and casts have no principals. Object references
			// use the separate address remapper, not a fabricated owner.
		case *Extension:
			n := *d
			var err error
			n.Owner, err = remap(d.Owner)
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *EventTrigger:
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
			n, err := procedure(d)
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, n)
		case *Language:
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
		case *OperatorFamily:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, OperatorFamilySeeded(d))
			if err != nil {
				return err
			}
			state.SetDescriptor(addr, &n)
		case *OperatorClass:
			n := *d
			var err error
			n.Owner, err = seedOwner(d.Owner, OperatorClassSeeded(d))
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

func principalMappings(m PrincipalRemap) (func(authorization.ACLValue) (authorization.ACLValue, error), ProcedureRemapper) {
	acl := func(a authorization.ACLValue) (authorization.ACLValue, error) {
		if len(a.Entries) == 0 {
			return a, nil
		}
		n := authorization.ACLValue{Explicit: a.Explicit, Entries: make([]authorization.ACLEntry, len(a.Entries))}
		for i, x := range a.Entries {
			var err error
			x.Grantor, err = m.Principal(x.Grantor)
			if err != nil {
				return authorization.ACLValue{}, err
			}
			if !x.Grantee.Public {
				x.Grantee.Role, err = m.Principal(x.Grantee.Role)
				if err != nil {
					return authorization.ACLValue{}, err
				}
			}
			n.Entries[i] = x
		}
		return n, nil
	}
	var procedures map[*Procedure]*Procedure
	var procedure ProcedureRemapper
	procedure = func(d *Procedure) (*Procedure, error) {
		if d == nil {
			return nil, nil
		}
		if n := procedures[d]; n != nil {
			return n, nil
		}
		n := *d
		var err error
		n.Owner, err = m.SeededOwner(d.Owner, d.Seeded() || d.Foreign != nil)
		if err != nil {
			return nil, err
		}
		n.ACL, err = acl(d.ACL)
		if err != nil {
			return nil, err
		}
		if procedures == nil {
			procedures = make(map[*Procedure]*Procedure)
		}
		procedures[d] = &n
		procedures[&n] = &n
		if len(d.Body) != 0 {
			n.Body = append([]RoutineBodyEdge(nil), d.Body...)
			for i, edge := range d.Body {
				recurse := procedure
				if m.Procedure != nil {
					recurse = m.Procedure
				}
				n.Body[i].Routine, err = recurse(edge.Routine)
				if err != nil {
					return nil, err
				}
			}
		}
		return &n, nil
	}
	return acl, procedure
}
