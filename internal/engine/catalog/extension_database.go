package catalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

var CurrentDatabaseAddress = ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 16383}

type Database struct {
	ObjectAddress  ids.ObjectAddress
	ObjectRevision ids.DefinitionRevision
	ObjectName     string
	Owner          authorization.PrincipalRef
	ACL            authorization.ACLValue
}

func (d *Database) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Database) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (*Database) Namespace() ids.ObjectAddress { return ids.ObjectAddress{} }

func (d *Database) Name() string { return d.ObjectName }

func (*Database) Kind() ids.ObjectKind { return ids.DatabaseObject }

type DatabaseProvider struct{}

func (DatabaseProvider) ClassID() ids.OID { return ids.DatabaseClassID }

func (DatabaseProvider) Validate(v Read, d Descriptor) error {
	db, ok := d.(*Database)
	if !ok || db.Address() != CurrentDatabaseAddress || db.Revision() == 0 || v.NameAddress(DescriptorNameKey(d)) != db.Address() || db.Namespace() != (ids.ObjectAddress{}) {
		return fmt.Errorf("stuntgres: invalid current database descriptor")
	}
	return nil
}
