package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// SQL/MED global objects are ordinary immutable catalog descriptors. Mapping
// names encode exact server/role identities, never role membership or SQL text.
type foreignDescriptor struct {
	address                             ids.ObjectAddress
	revision                            ids.DefinitionRevision
	name                                string
	owner                               authorization.PrincipalRef
	acl                                 authorization.ACLValue
	options                             []ForeignOption
	wrapper, server, handler, validator ids.ObjectAddress
	mappedRole                          authorization.PrincipalRef // zero is PUBLIC
	mappedName                          string                     // diagnostic spelling only; identity is mappedRole
	serverType, version                 *string
}

func (d *foreignDescriptor) Address() ids.ObjectAddress       { return d.address }
func (d *foreignDescriptor) Revision() ids.DefinitionRevision { return d.revision }
func (*foreignDescriptor) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *foreignDescriptor) Name() string                     { return d.name }
func (d *foreignDescriptor) Kind() ids.ObjectKind {
	switch d.address.ClassID {
	case ForeignWrapperClassID:
		return ids.ForeignWrapperObject
	case ForeignServerClassID:
		return ids.ForeignServerObject
	default:
		return ids.ForeignMappingObject
	}
}

func foreignMappingName(server ids.ObjectAddress, role authorization.PrincipalRef) string {
	return strconv.FormatUint(uint64(server.ObjectID), 10) + ":" + strconv.FormatUint(uint64(role.ID()), 10)
}

type foreignTableDefinition struct {
	server  ids.ObjectAddress
	options []ForeignOption
	columns map[int32][]ForeignOption
}

type foreignCatalogProvider struct{ class ids.OID }

func (p foreignCatalogProvider) ClassID() ids.OID { return p.class }
func (p foreignCatalogProvider) Validate(v CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*foreignDescriptor)
	if !ok || d.address.ClassID != p.class || d.address.ObjectID == 0 || d.address.SubID != 0 || d.revision == 0 || v.state.names[descriptorNameKey(d)] != d.address {
		return fmt.Errorf("stuntgres: invalid SQL/MED descriptor")
	}
	switch p.class {
	case ForeignWrapperClassID:
		for _, a := range []ids.ObjectAddress{d.handler, d.validator} {
			if a != (ids.ObjectAddress{}) {
				if _, ok := v.state.descriptors[a].(*procedureDescriptor); !ok {
					return fmt.Errorf("stuntgres: missing SQL/MED procedure identity")
				}
			}
		}
	case ForeignServerClassID:
		w, ok := v.state.descriptors[d.wrapper].(*foreignDescriptor)
		if !ok || w.address.ClassID != ForeignWrapperClassID {
			return fmt.Errorf("stuntgres: missing foreign-data wrapper")
		}
	case ForeignMappingClassID:
		s, ok := v.state.descriptors[d.server].(*foreignDescriptor)
		if !ok || s.address.ClassID != ForeignServerClassID || d.name != foreignMappingName(d.server, d.mappedRole) {
			return fmt.Errorf("stuntgres: invalid user mapping identity")
		}
	default:
		return fmt.Errorf("stuntgres: unknown SQL/MED catalog class")
	}
	return nil
}
