package lock

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestCatalogPairNameLockResources(t *testing.T) {
	for _, class := range []ids.OID{ids.CastClassID, ids.TransformClassID} {
		t.Run(fmt.Sprint(class), func(t *testing.T) {
			f := newLockFixture(t)
			a, b := f.tx(), f.tx()
			key := Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: class, Name: "23,50000"}}
			for _, name := range []string{"", "23", "23,", ",23", "0,23", "23,0", "023,23", "23,023", "-1,23", "23,+1", "23, 1", "23,1,2", "4294967296,23", "23,4294967296", "２３,1", "23,1\x00"} {
				bad := key
				bad.Name.Name = name
				if request, err := f.m.StartAcquire(context.Background(), a, bad, AccessExclusive, Policy{}); err == nil || request != nil {
					t.Fatal("malformed catalog pair name admitted", name, err)
				}
			}
			for _, mutate := range []func(*Resource){
				func(r *Resource) { r.Name.Namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}; r.Address = r.Name.Namespace },
				func(r *Resource) { r.Name.Signature = "23" },
				func(r *Resource) { r.Address = ids.ObjectAddress{ClassID: class, ObjectID: 50000} },
				func(r *Resource) { r.ObjectRef = new(ids.Domain).Ref(f.epoch, r.Address) },
				func(r *Resource) { r.Epoch++ },
				func(r *Resource) { r.Row = 1 },
				func(r *Resource) { r.RoleTuple = true },
			} {
				bad := key
				mutate(&bad)
				if request, err := f.m.StartAcquire(context.Background(), a, bad, AccessExclusive, Policy{}); err == nil || request != nil {
					t.Fatal("invalid catalog pair resource admitted", bad, err)
				}
			}
			if request, err := f.m.StartAcquire(context.Background(), a, key, AccessShare, Policy{}); err == nil || request != nil {
				t.Fatal("nonexclusive catalog pair claim admitted", err)
			}
			for _, name := range []string{"1,1", "4294967295,4294967295"} {
				valid := key
				valid.Name.Name = name
				if f.request(a, valid, AccessExclusive) != nil {
					t.Fatal("canonical uint32 boundary did not acquire", name)
				}
			}
			if f.request(a, key, AccessExclusive) != nil {
				t.Fatal("uncontended catalog pair claim queued")
			}
			for _, name := range []string{"50000,23", "23,50001", "24,50000"} {
				other := key
				other.Name.Name = name
				if f.request(b, other, AccessExclusive) != nil {
					t.Fatal("different catalog pair serialized", name)
				}
			}
			other := key
			other.Name.ClassID = ids.TransformClassID
			if class == ids.TransformClassID {
				other.Name.ClassID = ids.CastClassID
			}
			if f.request(b, other, AccessExclusive) != nil {
				t.Fatal("different catalog class serialized")
			}
			waiting := f.request(b, key, AccessExclusive)
			if waiting == nil {
				t.Fatal("same catalog pair did not queue")
			}
			a.MirrorFullXID(42)
			pids, err := f.m.BlockingPids(f.domain, b.Session().ID())
			if err != nil || len(pids) != 1 || pids[0] != int32(a.Session().ID()) {
				t.Fatal("pair claim lost actual blocker", pids, err)
			}
			records, err := f.m.PGLocks(f.domain, f.epoch)
			if err != nil {
				t.Fatal(err)
			}
			seenWait := false
			for _, record := range records {
				if record.Kind == RelationRecord || record.Kind == ObjectRecord || record.Kind == AdvisoryRecord {
					t.Fatal("pair claim fabricated a physical resource", record)
				}
				if record.Waiting && record.Kind == TransactionRecord && record.XID == 42 && record.Owner.Session == b.Session().ID() {
					seenWait = true
				}
			}
			if !seenWait {
				t.Fatal("pair claim lost transaction-completion dependency")
			}
			f.m.End(a)
			if err := f.result(waiting); err != nil {
				t.Fatal(err)
			}
			f.m.End(b)
			f.empty()
		})
	}
}

func TestTransformDefinitionLockResources(t *testing.T) {
	f := newLockFixture(t)
	writer, reader := f.tx(), f.tx()
	pair := Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{ClassID: ids.TransformClassID, ObjectID: 50000})}
	for _, mutate := range []func(*Resource){
		func(r *Resource) { r.Address.SubID = 1 },
		func(r *Resource) { r.Address.ObjectID = 0 },
		func(r *Resource) { r.Row = 1 },
		func(r *Resource) { r.RoleTuple = true },
		func(r *Resource) { r.ObjectRef = new(ids.Domain).Ref(f.epoch, r.Address) },
		func(r *Resource) { r.Epoch++ },
	} {
		bad := pair
		mutate(&bad)
		if request, err := f.m.StartAcquire(context.Background(), writer, bad, AccessShare, Policy{}); err == nil || request != nil {
			t.Fatal("invalid transform definition resource admitted", bad, err)
		}
	}
	if request, err := f.m.StartAcquire(context.Background(), writer, pair, RowExclusive, Policy{}); err == nil || request != nil {
		t.Fatal("relation write mode admitted for transform", err)
	}
	if f.request(writer, pair, AccessExclusive) != nil {
		t.Fatal("uncontended transform definition queued")
	}
	waiting := f.request(reader, pair, AccessShare)
	if waiting == nil {
		t.Fatal("transform reader bypassed retiring definition")
	}
	record, err := pgResourceRecord(pair, RecordOwner{}, AccessExclusive)
	if err != nil || record.Kind != ObjectRecord || record.Class != uint32(ids.TransformClassID) || record.Object != 50000 || record.Shared {
		t.Fatal("transform definition did not project its database-local object", record, err)
	}
	records, err := f.m.PGLocks(f.domain, f.epoch)
	if err != nil {
		t.Fatal(err)
	}
	holders, waiters := 0, 0
	for _, record := range records {
		if record.Kind == RelationRecord {
			t.Fatal("transform fabricated a relation resource", record)
		}
		if record.Kind != ObjectRecord || record.Class != uint32(ids.TransformClassID) {
			continue
		}
		if record.Object != 50000 || record.Shared {
			t.Fatal("transform object identity changed", record)
		}
		if record.Waiting {
			waiters++
		} else {
			holders++
		}
	}
	if holders != 1 || waiters != 1 {
		t.Fatal("transform object holder/waiter projection", holders, waiters)
	}
	f.m.End(writer)
	if err := f.result(waiting); err != nil {
		t.Fatal(err)
	}
	f.m.End(reader)
	f.empty()
}
