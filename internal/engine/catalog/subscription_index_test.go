package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

// Like the other derived-index tests, compare the read index with the actual
// descriptor map, including pointer identity and retained immutable roots.
func checkSubscriptionIndex(t *testing.T, state *State, label string) {
	t.Helper()
	want := map[ids.ObjectAddress]*Subscription{}
	for address, descriptor := range state.Descriptors() {
		if subscription, ok := descriptor.(*Subscription); ok {
			want[address] = subscription
		}
	}
	got := map[ids.ObjectAddress]*Subscription{}
	for address, subscription := range NewRead(nil, state).Subscriptions() {
		if _, duplicate := got[address]; duplicate {
			t.Fatalf("%s: duplicate subscription %v", label, address)
		}
		got[address] = subscription
	}
	if len(got) != len(want) {
		t.Fatalf("%s: subscription index has %d entries, want %d", label, len(got), len(want))
	}
	for address, subscription := range want {
		if got[address] != subscription {
			t.Fatalf("%s: subscription %v = %p, want %p", label, address, got[address], subscription)
		}
	}
}

func TestCatalogSubscriptionIndexClonesAndCandidates(t *testing.T) {
	base, _, relation := catalogReadFixture()
	// Shared subscriptions from different databases must both survive unrelated
	// candidate publication, regardless of slot association.
	kept := &Subscription{
		ObjectAddress:  ids.ObjectAddress{ClassID: ids.SubscriptionClassID, ObjectID: 3001},
		ObjectRevision: 1, ObjectName: "kept",
		Database: ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 4001},
		ReplicationOriginID: 1,
	}
	changed := &Subscription{
		ObjectAddress:  ids.ObjectAddress{ClassID: ids.SubscriptionClassID, ObjectID: 3002},
		ObjectRevision: 1, ObjectName: "changed", HasSlot: true, SlotName: "changed",
		Database: ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 4002},
		ReplicationOriginID: 2,
	}
	for _, subscription := range []*Subscription{kept, changed} {
		base.SetDescriptor(subscription.Address(), subscription)
		base.BindName(DescriptorNameKey(subscription), subscription.Address())
	}
	clone := CloneState(base)
	checkSubscriptionIndex(t, clone, "clone")
	unrelated := NewCandidate(NewRead(nil, clone))
	nextRelation := *relation
	nextRelation.ObjectRevision++
	if err := unrelated.Replace(&nextRelation); err != nil {
		t.Fatal(err)
	}
	published := unrelated.ApplyTo(base)
	checkSubscriptionIndex(t, published, "unrelated candidate publication")

	candidate := NewCandidate(NewRead(nil, published))
	nextSubscription := *changed
	nextSubscription.ObjectRevision++
	nextSubscription.ObjectName = "reassigned"
	if err := candidate.Replace(&nextSubscription); err != nil {
		t.Fatal(err)
	}
	candidate.RecordRemoval(ids.ObjectRef{Address: kept.Address()}, kept)
	checkSubscriptionIndex(t, candidate.State(), "candidate replacement and removal")
	applied := candidate.ApplyTo(published)
	checkSubscriptionIndex(t, applied, "published replacement and removal")
	if applied.Get(kept.Address()) != nil || applied.Get(changed.Address()) != &nextSubscription {
		t.Fatal("explicit subscription replacement/removal lost")
	}
	checkSubscriptionIndex(t, base, "retained base")
	checkSubscriptionIndex(t, clone, "retained clone")
	checkSubscriptionIndex(t, published, "retained publication")
	if published.Get(kept.Address()) != kept || published.Get(changed.Address()) != changed {
		t.Fatal("subscription mutation changed retained publication")
	}
}
