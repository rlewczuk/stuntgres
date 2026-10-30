package catalog

import "stuntgres/internal/engine/ids"

// RetainOtherSubscriptionsLocked runs after imported owner/identity remapping.
// The selected database is replaced; every other database retains its actual
// immutable definitions and principal authority, including concurrent commits.
func (s *Store) RetainOtherSubscriptionsLocked(next *State) {
	for address, d := range s.State.subscriptions.All() {
		if d.Database == s.DatabaseAddress() {
			continue
		}
		if old := next.Get(address); old != nil {
			next.UnbindName(DescriptorNameKey(old))
		}
		next.SetDescriptor(address, d)
		next.BindName(DescriptorNameKey(d), address)
		for originAddress, origin := range s.State.replicationOrigins.All() {
			if origin.Subscription != address {
				continue
			}
			if old := next.Get(originAddress); old != nil {
				next.UnbindName(DescriptorNameKey(old))
			}
			next.SetDescriptor(originAddress, origin)
			next.BindName(DescriptorNameKey(origin), originAddress)
		}
	}
}

// InheritSubscriptionsLocked attaches a new database to current shared state,
// preserving each subscription's exact immutable origin registration.
func (s *Store) InheritSubscriptionsLocked(next *State) {
	for address, descriptor := range next.Descriptors() {
		if address.ClassID == ids.SubscriptionClassID || address.ClassID == ids.ReplicationOriginClassID {
			next.UnbindName(DescriptorNameKey(descriptor))
			next.RemoveDescriptor(address)
		}
	}
	for address, d := range s.State.subscriptions.All() {
		next.SetDescriptor(address, d)
		next.BindName(DescriptorNameKey(d), address)
	}
	for address, d := range s.State.replicationOrigins.All() {
		next.SetDescriptor(address, d)
		next.BindName(DescriptorNameKey(d), address)
	}
}
