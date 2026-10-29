package catalog

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// SQL/MED global objects are ordinary immutable catalog descriptors. Mapping
// names encode exact server/role identities, never role membership or SQL text.
type ForeignObject struct {
	ObjectAddress                       ids.ObjectAddress
	Extension                           ids.ObjectAddress
	ObjectRevision                      ids.DefinitionRevision
	ObjectName                          string
	Owner                               authorization.PrincipalRef
	ACL                                 authorization.ACLValue
	Options                             []ForeignOption
	Wrapper, Server, Handler, Validator ids.ObjectAddress
	MappedRole                          authorization.PrincipalRef // zero is PUBLIC
	MappedName                          string                     // diagnostic spelling only; identity is mappedRole
	ServerType, Version                 *string
}

func (d *ForeignObject) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *ForeignObject) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (*ForeignObject) Namespace() ids.ObjectAddress { return ids.ObjectAddress{} }

func (d *ForeignObject) Name() string { return d.ObjectName }

func (d *ForeignObject) Kind() ids.ObjectKind {
	switch d.Address().ClassID {
	case ids.ForeignWrapperClassID:
		return ids.ForeignWrapperObject
	case ids.ForeignServerClassID:
		return ids.ForeignServerObject
	default:
		return ids.ForeignMappingObject
	}
}

func ForeignMappingName(server ids.ObjectAddress, role authorization.PrincipalRef) string {
	return strconv.FormatUint(uint64(server.ObjectID), 10) + ":" + strconv.FormatUint(uint64(role.ID()), 10)
}

type ForeignTableDefinition struct {
	Server  ids.ObjectAddress
	Options []ForeignOption
	Columns map[int32][]ForeignOption
}

type ForeignCatalogProvider struct{ Class ids.OID }

func (p ForeignCatalogProvider) ClassID() ids.OID { return p.Class }

func (p ForeignCatalogProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*ForeignObject)
	if !ok || d.Address().ClassID != p.Class || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return fmt.Errorf("stuntgres: invalid SQL/MED descriptor")
	}
	switch p.Class {
	case ids.ForeignWrapperClassID:
		for _, a := range []ids.ObjectAddress{d.Handler, d.Validator} {
			if a != (ids.ObjectAddress{}) {
				if _, ok := v.Procedure(a); !ok {
					return fmt.Errorf("stuntgres: missing SQL/MED procedure identity")
				}
			}
		}
	case ids.ForeignServerClassID:
		w, ok := v.Foreign(d.Wrapper)
		if !ok || w.Address().ClassID != ids.ForeignWrapperClassID {
			return fmt.Errorf("stuntgres: missing foreign-data wrapper")
		}
	case ids.ForeignMappingClassID:
		s, ok := v.Foreign(d.Server)
		if !ok || s.Address().ClassID != ids.ForeignServerClassID || d.Name() != ForeignMappingName(d.Server, d.MappedRole) {
			return fmt.Errorf("stuntgres: invalid user mapping identity")
		}
	default:
		return fmt.Errorf("stuntgres: unknown SQL/MED catalog class")
	}
	return nil
}

// ForeignLabel is the object kind PostgreSQL diagnostics name for a SQL/MED
// class: a wrapper, server, user mapping or foreign table.
func ForeignLabel(class ids.OID) string {
	switch class {
	case ids.ForeignWrapperClassID:
		return "foreign-data wrapper"
	case ids.ForeignServerClassID:
		return "server"
	case ids.ForeignMappingClassID:
		return "user mapping"
	default:
		return "foreign table"
	}
}
