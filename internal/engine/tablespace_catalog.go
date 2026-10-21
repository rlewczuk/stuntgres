package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func (e *Engine) initializeTablespaces() {
	owner := e.roles.domain.Principal(authorization.BootstrapPrincipalID)
	for _, builtin := range catalog.BuiltinTablespaces {
		d := &catalog.Tablespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.OID}, ObjectRevision: 1, ObjectName: builtin.Name, Owner: owner}
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
}
