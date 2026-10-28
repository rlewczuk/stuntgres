package catalog

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
	}
}

// InheritSubscriptionsLocked attaches a new database to current shared state.
func (s *Store) InheritSubscriptionsLocked(next *State) {
	for address, d := range s.State.subscriptions.All() {
		next.SetDescriptor(address, d)
		next.BindName(DescriptorNameKey(d), address)
	}
}
