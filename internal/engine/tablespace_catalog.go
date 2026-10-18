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
	ObjectAddress  ids.ObjectAddress
	ObjectRevision ids.DefinitionRevision
	ObjectName     string
	Owner          authorization.PrincipalRef
	ACL            authorization.ACLValue
}

func (d *tablespaceDescriptor) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *tablespaceDescriptor) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*tablespaceDescriptor) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *tablespaceDescriptor) Name() string                     { return d.ObjectName }
func (*tablespaceDescriptor) Kind() ids.ObjectKind               { return ids.TablespaceObject }

type tablespaceProvider struct{}

func (tablespaceProvider) ClassID() ids.OID { return ids.TablespaceClassID }
func (tablespaceProvider) Validate(v CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*tablespaceDescriptor)
	if !ok || d == nil || v.state == nil || v.Get(d.Address()) != d {
		return fmt.Errorf("stuntgres: invalid tablespace descriptor authority")
	}
	valid := false
	for _, builtin := range builtinTablespaces {
		valid = valid || d.Address().ObjectID == builtin.oid && d.Name() == builtin.name
	}
	if !valid || d.Address().ClassID != ids.TablespaceClassID || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(descriptorNameKey(d)) != d.Address() {
		return fmt.Errorf("stuntgres: invalid tablespace descriptor")
	}
	return nil
}

func (e *Engine) initializeTablespaces() {
	owner := e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)
	for _, builtin := range builtinTablespaces {
		d := &tablespaceDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.oid}, ObjectRevision: 1, ObjectName: builtin.name, Owner: owner}
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(descriptorNameKey(d), d.Address())
	}
}

func resolveGrantTablespace(v AuthorizationView, name string) (ids.ObjectRef, error) {
	address := v.catalog.NameAddress(ids.NameKey{ClassID: ids.TablespaceClassID, Name: name})
	if address == (ids.ObjectAddress{}) {
		return ids.ObjectRef{}, sqlError("42704", fmt.Sprintf(`tablespace "%s" does not exist`, name))
	}
	return v.engine.catalog.Domain.Ref(v.catalog.Epoch(), address), nil
}
