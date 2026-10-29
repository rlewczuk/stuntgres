package lock

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestPublicationDefinitionLockResources(t *testing.T) {
	f := newLockFixture(t)
	writer, reader := f.tx(), f.tx()
	definition := Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{ClassID: ids.PublicationClassID, ObjectID: 50000})}
	if request := f.request(writer, definition, AccessExclusive); request != nil {
		t.Fatal("uncontended publication definition did not acquire")
	}
	waiting := f.request(reader, definition, AccessShare)
	if waiting == nil {
		t.Fatal("publication consumer bypassed retiring definition")
	}
	record, err := pgResourceRecord(definition, RecordOwner{}, AccessExclusive)
	if err != nil || record.Kind != ObjectRecord || record.Class != uint32(ids.PublicationClassID) || record.Object != 50000 || record.Shared {
		t.Fatal("publication is not a local definition lock", record, err)
	}
	for _, mutate := range []func(*Resource){
		func(r *Resource) { r.Address.SubID = 1 }, func(r *Resource) { r.Address.ObjectID = 0 }, func(r *Resource) { r.Row = 1 }, func(r *Resource) { r.RoleTuple = true }, func(r *Resource) { r.Epoch++ }, func(r *Resource) { r.ObjectRef = new(ids.Domain).Ref(f.epoch, r.Address) },
	} {
		invalid := definition
		mutate(&invalid)
		if request, err := f.m.StartAcquire(context.Background(), writer, invalid, AccessShare, Policy{}); err == nil || request != nil {
			t.Fatal("invalid publication definition admitted", invalid, err)
		}
	}
	if request, err := f.m.StartAcquire(context.Background(), writer, definition, RowExclusive, Policy{}); err == nil || request != nil {
		t.Fatal("publication admitted a relation lock mode", err)
	}
	f.m.End(writer)
	if err := f.result(waiting); err != nil {
		t.Fatal(err)
	}
	name := Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: ids.PublicationClassID, Name: "publication_name"}}
	if request := f.request(reader, name, AccessExclusive); request != nil {
		t.Fatal("publication name did not acquire")
	}
	for _, mutate := range []func(*Resource){
		func(r *Resource) {
			r.Name.Namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			r.ObjectRef = f.domain.Ref(f.epoch, r.Name.Namespace)
		},
		func(r *Resource) { r.Name.Signature = "not-overloaded" }, func(r *Resource) { r.Name.Name = "" }, func(r *Resource) { r.Epoch++ }, func(r *Resource) { r.ObjectRef = new(ids.Domain).Ref(f.epoch, ids.ObjectAddress{}) },
	} {
		invalid := name
		mutate(&invalid)
		if request, err := f.m.StartAcquire(context.Background(), reader, invalid, AccessExclusive, Policy{}); err == nil || request != nil {
			t.Fatal("invalid publication name admitted", invalid, err)
		}
	}
	f.m.End(reader)
	f.empty()
}
