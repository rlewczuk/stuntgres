package lock

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestLanguageDefinitionLockResources(t *testing.T) {
	f := newLockFixture(t)
	writer, reader := f.tx(), f.tx()
	language := Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 50000})}
	if request := f.request(writer, language, AccessExclusive); request != nil {
		t.Fatal("uncontended language definition did not acquire")
	}
	waiting := f.request(reader, language, AccessShare)
	if waiting == nil {
		t.Fatal("language consumer bypassed retiring definition")
	}
	record, err := pgResourceRecord(language, RecordOwner{}, AccessExclusive)
	if err != nil || record.Kind != ObjectRecord || record.Class != uint32(ids.LanguageClassID) || record.Object != 50000 || record.Shared {
		t.Fatal("database-global language became a relation or cluster-shared lock", record, err)
	}
	for _, mutate := range []func(*Resource){
		func(r *Resource) { r.Address.SubID = 1 },
		func(r *Resource) { r.Row = 1 },
		func(r *Resource) { r.RoleTuple = true },
		func(r *Resource) { r.Address.ObjectID = 0 },
	} {
		invalid := language
		mutate(&invalid)
		if request, err := f.m.StartAcquire(context.Background(), writer, invalid, AccessShare, Policy{}); err == nil || request != nil {
			t.Fatal("invalid global definition resource admitted", invalid, err)
		}
	}
	f.m.End(writer)
	if err := f.result(waiting); err != nil {
		t.Fatal(err)
	}
	name := Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: ids.LanguageClassID, Name: "language_name"}}
	if request := f.request(reader, name, AccessExclusive); request != nil {
		t.Fatal("global language name did not acquire")
	}
	invalid := name
	invalid.Name.Namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	invalid.ObjectRef = f.domain.Ref(f.epoch, invalid.Name.Namespace)
	if request, err := f.m.StartAcquire(context.Background(), reader, invalid, AccessExclusive, Policy{}); err == nil || request != nil {
		t.Fatal("schema-qualified global language name admitted", err)
	}
	f.m.End(reader)
	f.empty()
}
