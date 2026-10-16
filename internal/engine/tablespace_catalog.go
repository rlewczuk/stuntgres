package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
)

// These are real builtin identities; no physical location/provider is implied.
var builtinTablespaces = [...]struct {
	oid  OID
	name string
}{{1663, "pg_default"}, {1664, "pg_global"}}

type tablespaceDescriptor struct {
	address  ObjectAddress
	revision DefinitionRevision
	name     string
	owner    authorization.PrincipalRef
	acl      authorization.ACLValue
}

func (d *tablespaceDescriptor) Address() ObjectAddress       { return d.address }
func (d *tablespaceDescriptor) Revision() DefinitionRevision { return d.revision }
func (*tablespaceDescriptor) Namespace() ObjectAddress       { return ObjectAddress{} }
func (d *tablespaceDescriptor) Name() string                 { return d.name }
func (*tablespaceDescriptor) Kind() ObjectKind               { return TablespaceObject }

type tablespaceProvider struct{}

func (tablespaceProvider) ClassID() OID { return TablespaceClassID }
func (tablespaceProvider) Validate(v CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*tablespaceDescriptor)
	if !ok || d == nil || v.state == nil || v.state.descriptors[d.address] != d {
		return fmt.Errorf("stuntgres: invalid tablespace descriptor authority")
	}
	valid := false
	for _, builtin := range builtinTablespaces {
		valid = valid || d.address.ObjectID == builtin.oid && d.name == builtin.name
	}
	if !valid || d.address.ClassID != TablespaceClassID || d.address.SubID != 0 || d.revision == 0 || v.state.names[descriptorNameKey(d)] != d.address {
		return fmt.Errorf("stuntgres: invalid tablespace descriptor")
	}
	return nil
}

func (e *Engine) initializeTablespaces() {
	owner := e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)
	for _, builtin := range builtinTablespaces {
		d := &tablespaceDescriptor{address: ObjectAddress{ClassID: TablespaceClassID, ObjectID: builtin.oid}, revision: 1, name: builtin.name, owner: owner}
		e.state.descriptors[d.address] = d
		e.state.names[descriptorNameKey(d)] = d.address
	}
}

func resolveGrantTablespace(v AuthorizationView, name string) (ObjectRef, error) {
	address := v.catalog.state.names[NameKey{ClassID: TablespaceClassID, Name: name}]
	if address == (ObjectAddress{}) {
		return ObjectRef{}, sqlError("42704", fmt.Sprintf(`tablespace "%s" does not exist`, name))
	}
	return ObjectRef{owner: v.engine.owner, Epoch: v.catalog.Epoch(), Address: address}, nil
}
