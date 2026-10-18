package catalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// These are real builtin identities; no physical location/provider is implied.
var BuiltinTablespaces = [...]struct {
	OID  ids.OID
	Name string
}{{1663, "pg_default"}, {1664, "pg_global"}}

type Tablespace struct {
	ObjectAddress  ids.ObjectAddress
	ObjectRevision ids.DefinitionRevision
	ObjectName     string
	Owner          authorization.PrincipalRef
	ACL            authorization.ACLValue
}

func (d *Tablespace) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Tablespace) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (*Tablespace) Namespace() ids.ObjectAddress { return ids.ObjectAddress{} }

func (d *Tablespace) Name() string { return d.ObjectName }

func (*Tablespace) Kind() ids.ObjectKind { return ids.TablespaceObject }

type TablespaceProvider struct{}

func (TablespaceProvider) ClassID() ids.OID { return ids.TablespaceClassID }

func (TablespaceProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*Tablespace)
	if !ok || d == nil || v.state == nil || v.Get(d.Address()) != d {
		return fmt.Errorf("stuntgres: invalid tablespace descriptor authority")
	}
	valid := false
	for _, builtin := range BuiltinTablespaces {
		valid = valid || d.Address().ObjectID == builtin.OID && d.Name() == builtin.Name
	}
	if !valid || d.Address().ClassID != ids.TablespaceClassID || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return fmt.Errorf("stuntgres: invalid tablespace descriptor")
	}
	return nil
}
