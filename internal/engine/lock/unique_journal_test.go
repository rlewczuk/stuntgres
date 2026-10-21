package lock

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
)

// The journal is the manager's unique-key checkpoint and delta bound to one
// ticket: a restore drops only the claims granted after the checkpoint, and a
// delta restore drops only the claims granted while the delta was active.
func TestUniqueJournal(t *testing.T) {
	f := newLockFixture(t)
	owner := f.tx()
	table := f.domain.Ref(f.epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999999})
	key := func(hash uint64) Resource {
		r := Resource{ObjectRef: table}
		r.key = &UniqueKey{bucket: uniqueLockBucket{ObjectRef: table, hash: hash}}
		return r
	}
	claim := func(r Resource) {
		t.Helper()
		if err := f.m.Acquire(context.Background(), owner, r, UniqueClaim); err != nil {
			t.Fatal(err)
		}
	}
	journal := f.m.UniqueJournal(owner)
	first, second, third := key(1), key(2), key(3)
	claim(first)
	held := journal.Checkpoint(nil)
	claim(second)
	journal.Restore(held)
	if !f.m.Holds(owner, first, UniqueClaim) || f.m.Holds(owner, second, UniqueClaim) {
		t.Fatal("restore kept a later claim or dropped a checkpointed one")
	}
	journal.BeginDelta()
	claim(third)
	if deltas := f.m.Inspect(owner).UniqueDeltas[owner]; len(deltas) != 1 || deltas[0] != third {
		t.Fatal("delta did not journal the new claim", deltas)
	}
	journal.RestoreDelta()
	journal.EndDelta()
	if !f.m.Holds(owner, first, UniqueClaim) || f.m.Holds(owner, third, UniqueClaim) {
		t.Fatal("delta restore kept the new claim or dropped an older one")
	}
}
