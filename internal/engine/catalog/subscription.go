package catalog

import (
	"fmt"
	"iter"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"unicode/utf8"
)

// Subscription is shared cluster metadata, named within Database, not a schema.
// Publications owns its backing; callers can only obtain detached copies.
type Subscription struct {
	ObjectAddress                                          ids.ObjectAddress
	ObjectRevision                                         ids.DefinitionRevision
	ObjectName                                             string
	Database                                               ids.ObjectAddress
	Owner                                                  authorization.PrincipalRef
	Connection                                             string
	SlotName                                               string
	HasSlot                                                bool
	Enabled, Binary                                        bool
	Streaming, TwoPhase                                    byte
	DisableOnError, PasswordRequired, RunAsOwner, Failover bool
	SynchronousCommit, Origin                              string
	SkipLSN                                                uint64
	publications                                           []string
}

func (d *Subscription) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *Subscription) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*Subscription) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *Subscription) Name() string                     { return d.ObjectName }
func (*Subscription) Kind() ids.ObjectKind               { return ids.SubscriptionObject }
func (d *Subscription) Publications() []string           { return slices.Clone(d.publications) }
func (d *Subscription) SetPublications(names []string)   { d.publications = slices.Clone(names) }
func SubscriptionNameKey(database ids.ObjectAddress, name string) ids.NameKey {
	return ids.NameKey{ClassID: ids.SubscriptionClassID, Namespace: database, Name: name}
}

type SubscriptionProvider struct{}

func (SubscriptionProvider) ClassID() ids.OID { return ids.SubscriptionClassID }
func (SubscriptionProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*Subscription)
	invalid := func() error { return fmt.Errorf("stuntgres: invalid subscription descriptor authority") }
	if !ok || d == nil || v.Owner() == nil || v.State() == nil || v.Get(d.Address()) != d || d.Address().ClassID != ids.SubscriptionClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || d.Name() == "" || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return invalid()
	}
	if v.Owner().Principals == nil || d.Owner.ID() == 0 || d.Owner != v.Owner().Principals.Principal(d.Owner.ID()) || d.Database.ClassID != ids.DatabaseClassID || d.Database.SubID != 0 {
		return invalid()
	}
	if _, ok := v.Get(d.Database).(*Database); !ok {
		return invalid()
	}
	if len(d.Name()) > 63 || !utf8.ValidString(d.Name()) || strings.IndexByte(d.Name(), 0) >= 0 || d.HasSlot && (len(d.SlotName) == 0 || len(d.SlotName) > 63) || !d.HasSlot && d.SlotName != "" {
		return invalid()
	}
	if d.Enabled && !d.HasSlot || d.Streaming != 'f' && d.Streaming != 't' && d.Streaming != 'p' || d.TwoPhase != 'd' && d.TwoPhase != 'p' || len(d.publications) == 0 {
		return invalid()
	}
	for i, name := range d.publications {
		if name == "" || len(name) > 63 || !utf8.ValidString(name) || strings.IndexByte(name, 0) >= 0 || slices.Contains(d.publications[:i], name) {
			return invalid()
		}
	}
	return nil
}
func (r Read) Subscriptions() iter.Seq2[ids.ObjectAddress, *Subscription] {
	if r.state == nil {
		return func(func(ids.ObjectAddress, *Subscription) bool) {}
	}
	return r.state.subscriptions.All()
}
func (s *State) updateSubscriptions(old, next Descriptor) {
	a, was := old.(*Subscription)
	b, is := next.(*Subscription)
	if was && (!is || a.Address() != b.Address()) {
		s.subscriptions = s.subscriptions.Delete(a.Address())
	}
	if is {
		s.subscriptions = s.subscriptions.Set(b.Address(), b)
	}
}
