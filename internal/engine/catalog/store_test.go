package catalog

import (
	"errors"
	"math"
	"testing"

	"stuntgres/internal/engine/ids"
)

func storeFixture(nextOID ids.OID) *Store {
	state, _, _ := catalogReadFixture()
	s := NewStore(Owner{Providers: map[ids.OID]Provider{}}, nextOID, 2)
	s.PublishLocked(state)
	return &s
}

func TestNewStoreDomainsAndFrontiers(t *testing.T) {
	first, second := storeFixture(16384), storeFixture(16384)
	if first.Domain == nil || first.Domain == second.Domain {
		t.Fatal("stores share or lack a provenance domain")
	}
	if first.NextOID() != 16384 || first.NextRevision() != 2 {
		t.Fatal("frontiers", first.NextOID(), first.NextRevision())
	}
	if first.Current().Owner() != &first.Owner || first.Current().State() != first.State {
		t.Fatal("Current is not the published state of this store")
	}
}

func TestStoreOIDAllocator(t *testing.T) {
	s := storeFixture(ids.PGCatalogNamespaceOID)
	if n, err := s.AllocateOIDLocked(); err != nil || n != ids.PGCatalogNamespaceOID+1 {
		t.Fatalf("reserved OID skip: %d %v", n, err)
	}
	s.InstallOIDFrontierLocked(900)
	if n, err := s.AllocateOIDLocked(); err != nil || n != 902 {
		t.Fatalf("descriptor OID skip: %d %v", n, err)
	}
	reservation := s.OIDReservationLocked()
	if n, err := s.AllocateOIDFromLocked(&reservation.Next, 0); err != nil || n != 903 || reservation.Base != 903 || reservation.Next != 904 || s.NextOID() != 903 {
		t.Fatal("reservation advanced the live frontier", n, err, reservation, s.NextOID())
	}
	s.InstallOIDFrontierLocked(0)
	if _, err := s.AllocateOIDLocked(); !errors.Is(err, ErrObjectIdentityExhausted) {
		t.Fatalf("OID exhaustion: %v", err)
	}
}

func TestStoreOIDTerminalOccupancy(t *testing.T) {
	for _, class := range []ids.OID{ids.NamespaceClassID, ids.RelationClassID} {
		s := storeFixture(0)
		last := ^ids.OID(0)
		address := ids.ObjectAddress{ClassID: class, ObjectID: last}
		state := CloneState(s.State)
		if class == ids.NamespaceClassID {
			state.SetDescriptor(address, &Namespace{ObjectAddress: address, ObjectName: "last", ObjectRevision: 1})
		} else {
			state.SetDescriptor(address, &Relation{ObjectAddress: address, ObjectName: "last", ObjectRevision: 1, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900}})
		}
		s.PublishLocked(state)
		s.InstallOIDFrontierLocked(last)
		for range 2 {
			if _, err := s.AllocateOIDLocked(); err == nil || err.Error() != "stuntgres: catalog object identity exhausted" || s.NextOID() != 0 || s.State != state {
				t.Fatal("occupied terminal OID did not exhaust without publication", class, err)
			}
		}
	}
}

func TestStoreRevisionAllocator(t *testing.T) {
	s := storeFixture(16384)
	for _, want := range []ids.DefinitionRevision{2, 3} {
		if r, err := s.RevisionLocked(); err != nil || r != want {
			t.Fatal("revision", r, want, err)
		}
	}
	s.InstallRevisionFrontierLocked(ids.DefinitionRevision(^uint64(0)))
	for range 2 {
		if _, err := s.RevisionLocked(); err == nil || err.Error() != "stuntgres: catalog definition revision exhausted" || s.NextRevision() != ids.DefinitionRevision(^uint64(0)) {
			t.Fatalf("revision exhaustion: %v", err)
		}
	}
}

func TestStoreEnumMemberAllocator(t *testing.T) {
	s := storeFixture(16384)
	for _, want := range []int64{1, 3} {
		if got, err := s.AllocateEnumMemberLocked(); err != nil || got != want {
			t.Fatal("initial allocation", got, want, err)
		}
	}
	s.nextEnumMember = math.MaxUint32 - 2
	if got, err := s.AllocateEnumMemberLocked(); err != nil || got != math.MaxUint32 {
		t.Fatal("last OID", got, err)
	}
	for range 2 {
		if got, err := s.AllocateEnumMemberLocked(); got != 0 || err == nil || err.Error() != "stuntgres: enum member identity exhausted" || s.nextEnumMember != math.MaxUint32 {
			t.Fatal("exhaustion wrapped/aliased", got, err, s.nextEnumMember)
		}
	}
}
