package catalog

import (
	"maps"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// definitionOIDRelation owns one definition OID of every slice-held kind,
// starting at first.
func definitionOIDRelation(object, first ids.OID) *Relation {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: object}
	return &Relation{
		ObjectAddress: address, ObjectName: "defs", ObjectRevision: 1,
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900},
		Constraints: RelationConstraints{
			NotNull:     []NotNullConstraint{{OID: first}},
			Check:       []CheckConstraint{{OID: first + 1}},
			Unique:      []UniqueConstraint{{OID: first + 2}},
			Exclusion:   []ExclusionConstraint{{OID: first + 3}},
			ForeignKeys: []ForeignKeyConstraint{{OID: first + 4}},
		},
		ColumnMeta: ColumnMetadata{
			Defaults:    map[int32]ColumnDefault{1: {OID: first + 5}},
			Generations: map[int32]ColumnGeneration{2: {OID: first + 6}},
		},
		Security: RelationSecurity{Policies: []PolicyDefinition{{OID: first + 7}}},
	}
}

func definitionOIDDomain(object, first ids.OID) *Type {
	return &Type{
		SQLType: sqltype.LookupOnly(sqltype.ID(object), "defs_domain", sqltype.ID(object+1)), ObjectRevision: 1,
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900},
		DomainNotNull:   &DomainNotNullConstraint{OID: first},
		DomainChecks:    []DomainCheckConstraint{{OID: first + 1}},
	}
}

func publishDefinitionOIDs(s *Store, descriptors ...Descriptor) {
	state := CloneState(s.State)
	for _, d := range descriptors {
		state.SetDescriptor(d.Address(), d)
	}
	s.PublishLocked(state)
}

func allocateOIDs(t *testing.T, s *Store, n int) []ids.OID {
	t.Helper()
	got := make([]ids.OID, n)
	for i := range got {
		var err error
		if got[i], err = s.AllocateOIDLocked(); err != nil {
			t.Fatal(err)
		}
	}
	return got
}

// The allocator skips definition OIDs of the state it allocates against,
// including after a publication, an in-place write and during a bulk load.
func TestStoreOIDSkipsDefinitionOIDs(t *testing.T) {
	s := storeFixture(20000)
	if got := allocateOIDs(t, s, 2); got[0] != 20000 || got[1] != 20001 {
		t.Fatal("free candidates", got)
	}
	publishDefinitionOIDs(s, definitionOIDRelation(950, 20002))
	if got := allocateOIDs(t, s, 1); got[0] != 20010 {
		t.Fatal("published relation definitions", got)
	}
	// In place, as engine setup writes the published state.
	domain := definitionOIDDomain(951, 20011)
	s.State.SetDescriptor(domain.Address(), domain)
	if got := allocateOIDs(t, s, 1); got[0] != 20013 {
		t.Fatal("in-place domain definitions", got)
	}
	s.State.BeginBulkLoad(4)
	bulk := definitionOIDRelation(952, 20014)
	s.State.SetDescriptor(bulk.Address(), bulk)
	got := allocateOIDs(t, s, 1)
	s.State.EndBulkLoad()
	if got[0] != 20022 {
		t.Fatal("bulk-loaded relation definitions", got)
	}
	// The first allocation builds the set with 20024..20031 taken; the in-place
	// removal must then free them.
	publishDefinitionOIDs(s, definitionOIDRelation(953, 20024))
	if got := allocateOIDs(t, s, 1); got[0] != 20023 {
		t.Fatal("free candidate before published definitions", got)
	}
	s.State.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 953})
	if got := allocateOIDs(t, s, 1); got[0] != 20024 {
		t.Fatal("an in-place removal must free its definition OIDs", got)
	}
}

// Descriptor identities share the allocator's global space with owned
// definitions; only explicitly class-local large objects may overlap it.
func TestStoreOIDSkipsDescriptorOIDs(t *testing.T) {
	procedure := func(id ids.OID) Descriptor {
		return &Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}, ObjectName: "restored", ObjectRevision: 1}
	}
	for _, tc := range []struct {
		name string
		make func(ids.OID) Descriptor
	}{
		{"procedure", procedure},
		{"language", func(id ids.OID) Descriptor {
			return &Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: id}, ObjectName: "restored", ObjectRevision: 1}
		}},
		{"tablespace", func(id ids.OID) Descriptor {
			return &Tablespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: id}, ObjectName: "restored", ObjectRevision: 1}
		}},
		{"collation", func(id ids.OID) Descriptor { return &Collation{OID: id, ObjectName: "restored"} }},
		{"conversion", func(id ids.OID) Descriptor { return &Conversion{OID: id, ObjectName: "restored", ObjectRevision: 1} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := storeFixture(30000)
			// Prime the cache on a root without the restored descriptor.
			if got := allocateOIDs(t, s, 1); got[0] != 30000 {
				t.Fatal("initial free identity", got)
			}
			d := tc.make(30001)
			publishDefinitionOIDs(s, d, definitionOIDDomain(950, 30002))
			reservation := s.OIDReservationLocked()
			got, err := s.AllocateOIDFromLocked(&reservation.Next, 30001)
			if err != nil || got != 30004 || reservation.Base != 30001 || reservation.Next != 30005 || s.NextOID() != 30001 {
				t.Fatal("private reservation must skip descriptor and owned identities without advancing live frontier", got, err, reservation, s.NextOID())
			}
			if got := allocateOIDs(t, s, 1); got[0] != 30004 {
				t.Fatal("published descriptor and owned identities", got)
			}
			// In-place writes must invalidate a previously populated cache.
			d = tc.make(30005)
			s.State.SetDescriptor(d.Address(), d)
			if got := allocateOIDs(t, s, 1); got[0] != 30006 {
				t.Fatal("in-place descriptor identity", got)
			}
			s.State.BeginBulkLoad(2)
			d = tc.make(30007)
			s.State.SetDescriptor(d.Address(), d)
			if got := allocateOIDs(t, s, 1); got[0] != 30008 {
				t.Fatal("bulk-loaded descriptor identity", got)
			}
			s.State.EndBulkLoad()
			// Rebuild against the final map, then remove an incumbent in place.
			d = tc.make(30010)
			publishDefinitionOIDs(s, d)
			if got := allocateOIDs(t, s, 1); got[0] != 30009 {
				t.Fatal("free identity before incumbent", got)
			}
			s.State.RemoveDescriptor(d.Address())
			if got := allocateOIDs(t, s, 1); got[0] != 30010 {
				t.Fatal("removed incumbent still reserved", got)
			}
		})
	}
	t.Run("class_local_large_object", func(t *testing.T) {
		s := storeFixture(30000)
		address := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 30000}
		publishDefinitionOIDs(s, &LargeObject{ObjectAddress: address, ObjectName: "30000", ObjectRevision: 1})
		if err := ValidateDefinitionOIDs(s.Current()); err != nil {
			t.Fatal(err)
		}
		if got := allocateOIDs(t, s, 1); got[0] != 30000 {
			t.Fatal("class-local large object consumed global identity", got)
		}
		address.ObjectID = 30001
		publishDefinitionOIDs(s, procedure(30001), &LargeObject{ObjectAddress: address, ObjectName: "30001", ObjectRevision: 1})
		if err := ValidateDefinitionOIDs(s.Current()); err != nil {
			t.Fatal("class-local overlap with global descriptor", err)
		}
		if got := allocateOIDs(t, s, 1); got[0] != 30002 {
			t.Fatal("large-object overlap hid global incumbent", got)
		}
		s.State.BeginBulkLoad(2)
		address.ObjectID = 30003
		s.State.SetDescriptor(address, &LargeObject{ObjectAddress: address, ObjectName: "30003", ObjectRevision: 1})
		if got := allocateOIDs(t, s, 1); got[0] != 30003 {
			t.Fatal("bulk class-local large object consumed global identity", got)
		}
		s.State.EndBulkLoad()
	})
	t.Run("terminal_procedure", func(t *testing.T) {
		last := ^ids.OID(0)
		s := storeFixture(last)
		publishDefinitionOIDs(s, procedure(last))
		if got, err := s.AllocateOIDLocked(); got != 0 || err != ErrObjectIdentityExhausted || s.NextOID() != 0 {
			t.Fatal("occupied terminal descriptor did not exhaust", got, err, s.NextOID())
		}
	})
}

// DefinitionOIDUsed and ReserveDefinitionOIDs walk the same definitions.
func TestDefinitionOIDWalksAgree(t *testing.T) {
	for _, d := range []Descriptor{definitionOIDRelation(950, 30000), definitionOIDDomain(951, 30000)} {
		reserved := map[ids.OID]bool{}
		ReserveDefinitionOIDs(reserved, d)
		used := map[ids.OID]bool{}
		for id := ids.OID(29999); id < 30010; id++ {
			if DefinitionOIDUsed(d, id) {
				used[id] = true
			}
		}
		if len(reserved) == 0 || !maps.Equal(reserved, used) {
			t.Fatal("definition walks disagree", d.Address(), reserved, used)
		}
	}
}
