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
