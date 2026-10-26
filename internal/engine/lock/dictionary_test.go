package lock

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestDictionaryLockResources(t *testing.T) {
	for _, class := range []ids.OID{ids.TSTemplateClassID, ids.TSDictionaryClassID} {
		f := newLockFixture(t)
		reader, writer := f.tx(), f.tx()
		resource := Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{ClassID: class, ObjectID: 99001})}
		mark := f.m.Checkpoint(reader)
		if waiting := f.request(reader, resource, AccessShare); waiting != nil {
			t.Fatal("uncontended dictionary provider waited")
		}
		waiting := f.request(writer, resource, AccessExclusive)
		if waiting == nil {
			t.Fatal("dictionary retirement bypassed provider reader")
		}
		records, err := f.m.PGLocks(f.domain, f.epoch)
		if err != nil {
			t.Fatal(err)
		}
		holders, waiters := 0, 0
		readerOwner := RecordOwner{Session: reader.Session().ID(), Local: reader.ref.ID()}
		writerOwner := RecordOwner{Session: writer.Session().ID(), Local: writer.ref.ID()}
		for _, record := range records {
			if record.Kind == RelationRecord {
				t.Fatal("dictionary fabricated a relation tag", record)
			}
			if record.Kind != ObjectRecord || record.Class != uint32(class) {
				continue
			}
			if record.Object != 99001 || record.Subobject != 0 || record.Shared {
				t.Fatal("dictionary object identity or scope", record)
			}
			if record.Waiting {
				waiters++
				if record.Owner != writerOwner || record.Mode != AccessExclusive || record.WaitStart.IsZero() {
					t.Fatal("dictionary retirement waiter", record)
				}
			} else {
				holders++
				if record.Owner != readerOwner || record.Mode != AccessShare || !record.WaitStart.IsZero() {
					t.Fatal("dictionary provider holder", record)
				}
			}
		}
		if holders != 1 || waiters != 1 {
			t.Fatal("dictionary holder/waiter multiplicity", holders, waiters, records)
		}
		f.m.Restore(reader, mark)
		if err := f.result(waiting); err != nil {
			t.Fatal("dictionary grant after reader rollback", err)
		}
		f.m.Fence(f.domain, f.epoch+1)
		if snapshot := f.m.Inspect(); len(snapshot.Resources) != 0 || len(snapshot.Owners) != 0 {
			t.Fatal("dictionary replacement retained lock authority", snapshot)
		}
	}
}
