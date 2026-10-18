package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// These are real builtin identities; no physical location/provider is implied.
var builtinTablespaces = [...]struct {
	oid  ids.OID
	name string
}{{1663, "pg_default"}, {1664, "pg_global"}}

type tablespaceDescriptor struct {
	address  ids.ObjectAddress
	revision ids.DefinitionRevision
	name     string
	Owner    authorization.PrincipalRef
	ACL      authorization.ACLValue
}

func (d *tablespaceDescriptor) Address() ids.ObjectAddress       { return d.address }
func (d *tablespaceDescriptor) Revision() ids.DefinitionRevision { return d.revision }
func (*tablespaceDescriptor) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *tablespaceDescriptor) Name() string                     { return d.name }
func (*tablespaceDescriptor) Kind() ids.ObjectKind               { return ids.TablespaceObject }

type tablespaceProvider struct{}

func (tablespaceProvider) ClassID() ids.OID { return ids.TablespaceClassID }
func (tablespaceProvider) Validate(v CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*tablespaceDescriptor)
	if !ok || d == nil || v.state == nil || v.Get(d.address) != d {
		return fmt.Errorf("stuntgres: invalid tablespace descriptor authority")
	}
	valid := false
	for _, builtin := range builtinTablespaces {
		valid = valid || d.address.ObjectID == builtin.oid && d.name == builtin.name
	}
	if !valid || d.address.ClassID != ids.TablespaceClassID || d.address.SubID != 0 || d.revision == 0 || v.NameAddress(descriptorNameKey(d)) != d.address {
		return fmt.Errorf("stuntgres: invalid tablespace descriptor")
	}
	return nil
}

func (e *Engine) initializeTablespaces() {
	owner := e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)
	for _, builtin := range builtinTablespaces {
		d := &tablespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.oid}, revision: 1, name: builtin.name, Owner: owner}
		e.catalog.state.setDescriptor(d.address, d)
		e.catalog.state.bindName(descriptorNameKey(d), d.address)
	}
}

func resolveGrantTablespace(v AuthorizationView, name string) (ids.ObjectRef, error) {
	address := v.catalog.NameAddress(ids.NameKey{ClassID: ids.TablespaceClassID, Name: name})
	if address == (ids.ObjectAddress{}) {
		return ids.ObjectRef{}, sqlError("42704", fmt.Sprintf(`tablespace "%s" does not exist`, name))
	}
	return v.engine.catalog.Domain.Ref(v.catalog.Epoch(), address), nil
}
