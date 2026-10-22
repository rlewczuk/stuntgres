package index

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestEffectiveRoot(t *testing.T) {
	address, other := ids.ObjectAddress{ObjectID: 1}, ids.ObjectAddress{ObjectID: 2}
	committed, private := &storage.IndexRoot{}, &storage.IndexRoot{}
	roots := map[ids.ObjectAddress]*storage.IndexRoot{address: committed, other: committed}
	if got := EffectiveRoot(roots, nil, address); got != committed {
		t.Fatal("nil journal must read the committed root")
	}
	journal := &session.Journal{}
	if got := EffectiveRoot(roots, journal, address); got != committed {
		t.Fatal("empty journal must read the committed root")
	}
	JournalIndexes(journal).UniqueRoots = map[ids.ObjectAddress]*storage.IndexRoot{address: private}
	if got := EffectiveRoot(roots, journal, address); got != private {
		t.Fatal("private root must win")
	}
	if got := EffectiveRoot(roots, journal, other); got != committed {
		t.Fatal("missing private entry must read the committed root")
	}
	if got := EffectiveRoot(roots, journal, ids.ObjectAddress{ObjectID: 3}); got != nil {
		t.Fatal("unknown index must have no root")
	}
}
