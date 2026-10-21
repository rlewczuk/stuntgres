package catalog

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
)

// LargeObject is an immutable catalog descriptor; metadata and bytes publish as
// one journal entry. Its numeric identity is local to LargeObjectClassID.
type LargeObject struct {
	ObjectAddress  ids.ObjectAddress
	ObjectRevision ids.DefinitionRevision
	ObjectName     string
	Owner          authorization.PrincipalRef
	ACL            authorization.ACLValue
	Content        largeobject.Value
}

func (d *LargeObject) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *LargeObject) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*LargeObject) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *LargeObject) Name() string                     { return d.ObjectName }
func (*LargeObject) Kind() ids.ObjectKind               { return ids.LargeObjectObject }

type LargeObjectProvider struct{}

func (LargeObjectProvider) ClassID() ids.OID { return ids.LargeObjectClassID }
func (LargeObjectProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*LargeObject)
	if !ok || d == nil || v.State() == nil || v.Get(d.Address()) != d || d.Address().ClassID != ids.LargeObjectClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return fmt.Errorf("stuntgres: invalid large object descriptor authority")
	}
	if d.Name() != strconv.FormatUint(uint64(d.Address().ObjectID), 10) {
		return fmt.Errorf("stuntgres: invalid large object catalog name")
	}
	if v.Owner() == nil || v.Owner().Principals == nil || d.Owner.ID() == 0 || v.Owner().Principals.Principal(d.Owner.ID()) != d.Owner {
		return fmt.Errorf("stuntgres: foreign large object owner")
	}
	return d.Content.Validate()
}
