package catalog

import (
	"fmt"
	"iter"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/ids"
)

// ReplicationOrigin is the sole live registration authority. Subscription is an
// optional creation correlation, not a dependency or an alternative identity.
type ReplicationOrigin struct {
	ObjectAddress  ids.ObjectAddress
	ObjectRevision ids.DefinitionRevision
	ObjectName     string
	Subscription   ids.ObjectAddress
}

func (d *ReplicationOrigin) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *ReplicationOrigin) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*ReplicationOrigin) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *ReplicationOrigin) Name() string                     { return d.ObjectName }
func (*ReplicationOrigin) Kind() ids.ObjectKind               { return ids.ReplicationOriginObject }
func ReplicationOriginNameKey(name string) ids.NameKey {
	return ids.NameKey{ClassID: ids.ReplicationOriginClassID, Name: name}
}

type ReplicationOriginProvider struct{}

func (ReplicationOriginProvider) ClassID() ids.OID { return ids.ReplicationOriginClassID }
func (ReplicationOriginProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*ReplicationOrigin)
	if !ok || d == nil || v.Owner() == nil || v.State() == nil || v.Get(d.Address()) != d || d.Address().ClassID != ids.ReplicationOriginClassID || d.Address().ObjectID == 0 || d.Address().ObjectID >= 65535 || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(ReplicationOriginNameKey(d.Name())) != d.Address() || len(d.Name()) > 512 || !utf8.ValidString(d.Name()) || strings.IndexByte(d.Name(), 0) >= 0 {
		return fmt.Errorf("stuntgres: invalid replication origin registration authority")
	}
	if d.Subscription != (ids.ObjectAddress{}) {
		subscription, ok := v.Get(d.Subscription).(*Subscription)
		if !ok || d.Subscription.ClassID != ids.SubscriptionClassID || d.Subscription.SubID != 0 || d.Name() != subscription.ReplicationOriginName() {
			return fmt.Errorf("stuntgres: invalid replication origin subscription correlation")
		}
	}
	return nil
}
func (r Read) ReplicationOrigins() iter.Seq2[ids.ObjectAddress, *ReplicationOrigin] {
	if r.state == nil {
		return func(func(ids.ObjectAddress, *ReplicationOrigin) bool) {}
	}
	return r.state.replicationOrigins.All()
}
func (s *State) updateReplicationOrigins(old, next Descriptor) {
	a, was := old.(*ReplicationOrigin)
	b, is := next.(*ReplicationOrigin)
	if was && (!is || a.Address() != b.Address()) {
		s.replicationOrigins = s.replicationOrigins.Delete(a.Address())
	}
	if is {
		s.replicationOrigins = s.replicationOrigins.Set(b.Address(), b)
	}
}

// ValidateReplicationOrigins validates the complete live index, independently of
// subscription creation correlations that may outlive a public origin drop.
func ValidateReplicationOrigins(v Read) error {
	for _, d := range v.ReplicationOrigins() {
		if err := (ReplicationOriginProvider{}).Validate(v, d); err != nil {
			return err
		}
	}
	return nil
}

// StandaloneOriginsUnchanged fences a replacement admitted before external I/O.
// Peer subscription publications are independent and are retained at install.
func (s *State) StandaloneOriginsUnchanged(previous *State) bool {
	if s == previous {
		return true
	}
	if s == nil || previous == nil {
		return false
	}
	for address, d := range s.replicationOrigins.All() {
		if d.Subscription == (ids.ObjectAddress{}) && previous.Get(address) != d {
			return false
		}
	}
	for address, d := range previous.replicationOrigins.All() {
		if d.Subscription == (ids.ObjectAddress{}) && s.Get(address) != d {
			return false
		}
	}
	return true
}
