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
	FrozenXID      uint64
}

func (d *Database) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Database) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (*Database) Namespace() ids.ObjectAddress { return ids.ObjectAddress{} }

func (d *Database) Name() string { return d.ObjectName }

func (*Database) Kind() ids.ObjectKind { return ids.DatabaseObject }

// DatabaseAuthorityCompatible permits maintenance horizon drift, not a change
// to database identity, revision, ownership or the exact ACL authority.
func DatabaseAuthorityCompatible(current, observed Descriptor) bool {
	if current == observed {
		return true
	}
	a, aok := current.(*Database)
	b, bok := observed.(*Database)
	return aok && bok && a != nil && b != nil &&
		a.ObjectAddress == b.ObjectAddress &&
		a.ObjectRevision == b.ObjectRevision &&
		a.ObjectName == b.ObjectName &&
		a.Owner == b.Owner &&
		authorization.EqualACL(a.ACL, b.ACL)
}

type DatabaseProvider struct{}

func (DatabaseProvider) ClassID() ids.OID { return ids.DatabaseClassID }

func (DatabaseProvider) Validate(v Read, d Descriptor) error {
	db, ok := d.(*Database)
	if !ok || db.Address().ClassID != ids.DatabaseClassID || db.Address().ObjectID == 0 || db.Address().SubID != 0 || db.Revision() == 0 || v.NameAddress(DescriptorNameKey(d)) != db.Address() || db.Namespace() != (ids.ObjectAddress{}) {
		return fmt.Errorf("stuntgres: invalid database descriptor")
	}
	return nil
}
