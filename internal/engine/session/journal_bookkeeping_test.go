package session

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

type forgetSlot struct{ forgotten []ids.ObjectAddress }

func (*forgetSlot) IndexSlot()                         {}
func (s *forgetSlot) Clone() IndexSlot                 { return s }
func (s *forgetSlot) Forget(address ids.ObjectAddress) { s.forgotten = append(s.forgotten, address) }
func journalTestSchema(id ids.OID, name string) *catalog.Namespace {
	return &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: id}, ObjectName: name}
}

func TestJournalPrivateReadAndDescriptors(t *testing.T) {
	owner := &catalog.Owner{}
	schema := journalTestSchema(20001, "s")
	base := catalog.NewState(0)
	base.SetDescriptor(schema.Address(), schema)
	base.BindName(catalog.DescriptorNameKey(schema), schema.Address())

	if r := (*Journal)(nil).PrivateRead(owner, base); r.State() != base || r.Owner() != owner {
		t.Fatal("a nil journal did not read the base")
	}
	c := NewJournal()
	first := c.PrivateRead(owner, base)
	if first.State() != base || c.CachedBase != base || c.PrivateRead(owner, base) != first {
		t.Fatal("an empty journal did not read and cache the base")
	}

	slot := &forgetSlot{}
	c.Indexes = slot
	renamed := journalTestSchema(20002, "t")
	c.RecordDescriptor(base, renamed.Address(), renamed)
	c.RecordDescriptor(base, schema.Address(), nil)
	if c.Originals[schema.Address()] != schema || c.Originals[renamed.Address()] != nil ||
		c.Names[catalog.DescriptorNameKey(schema)] != schema.Address() || c.Names[catalog.DescriptorNameKey(renamed)] != (ids.ObjectAddress{}) {
		t.Fatal("descriptor changes did not record the originals and name bindings")
	}
	if !reflect.DeepEqual(slot.forgotten, []ids.ObjectAddress{schema.Address()}) || c.CachedBase != nil || c.Cached.State() != nil {
		t.Fatal("a removal did not forget its index state or invalidate the cache", slot.forgotten)
	}
	private := c.PrivateRead(owner, base)
	state := private.State()
	if state == base || state.Get(schema.Address()) != nil || state.Get(renamed.Address()) != renamed ||
		state.NameAddress(catalog.DescriptorNameKey(renamed)) != renamed.Address() || base.Get(schema.Address()) != schema {
		t.Fatal("the private read did not overlay the journal on a copy of the base")
	}
	if c.PrivateRead(owner, base) != private || c.PrivateRead(owner, catalog.CloneState(base)).State() == state {
		t.Fatal("the private read was not cached against its base")
	}
}

func TestJournalDefaultACLBookkeeping(t *testing.T) {
	base := catalog.NewState(0)
	schema := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20001}
	global := catalog.DefaultACLKey{Schema: ids.ObjectAddress{}, Class: authorization.DefaultTypes}
	inSchema := catalog.DefaultACLKey{Schema: schema, Class: authorization.DefaultTypes}
	c := NewJournal()
	c.Cached, c.CachedBase = catalog.NewRead(nil, base), base
	record := &catalog.DefaultACLRecord{}
	c.RecordDefaultACL(base, inSchema, record)
	if c.DefaultACLs[inSchema] != record || c.DefaultOriginals[inSchema] != nil || len(c.DefaultOriginals) != 1 || c.CachedBase != nil || c.Cached.State() != nil {
		t.Fatal("a default ACL change was not recorded with its original")
	}
	view := catalog.NewRead(nil, base)
	acl := c.ObserveCreationACL(base, view, authorization.PrincipalRef{}, schema, authorization.DefaultTypes)
	if _, observed := c.DefaultReads[global]; !observed || len(c.DefaultReads) != 1 {
		t.Fatal("creation did not observe only the committed default", c.DefaultReads)
	}
	if !reflect.DeepEqual(acl, catalog.CreationDefaultACL(view, authorization.PrincipalRef{}, schema, authorization.DefaultTypes)) {
		t.Fatal("creation ACL differs from the view's", acl)
	}
}
