package sequence

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// CopyGenerators detaches every sequence's generator and fails when one is
// missing; OwnTemporary keeps only the caller's own TEMP sequences.
func TestSequenceControlCopyAndRetain(t *testing.T) {
	f := newPreparedFixture(t)
	read := catalog.NewRead(f.owner, f.state)
	live := map[ids.ObjectAddress]*storage.SequenceGenerator{}
	for i, d := range []*catalog.Relation{f.plain, f.temporary, f.others} {
		live[d.Address()] = &storage.SequenceGenerator{Last: int64(i), Generation: 1}
	}
	copied, err := CopyGenerators(read, live)
	if err != nil || len(copied) != 3 {
		t.Fatal("copy", copied, err)
	}
	copied[f.plain.Address()].Last = 99
	if live[f.plain.Address()].Last != 0 || *copied[f.temporary.Address()] != *live[f.temporary.Address()] {
		t.Fatal("copies share or differ from the source generators")
	}
	delete(live, f.others.Address())
	if _, err := CopyGenerators(read, live); err == nil {
		t.Fatal("missing generator copied")
	}
	keep := OwnTemporary(read, f.caller)
	if !keep(f.temporary.Address()) || keep(f.plain.Address()) || keep(f.others.Address()) || keep(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 49999}) {
		t.Fatal("retained the wrong sequences")
	}
}
