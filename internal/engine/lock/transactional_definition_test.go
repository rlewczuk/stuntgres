package lock

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestTransactionalDefinitionLockResources(t *testing.T) {
	for _, class := range []ids.OID{ids.AccessMethodClassID, ids.OperatorFamilyClassID, ids.OperatorClassCatalogID, ids.AccessMethodOperatorClassID, ids.AccessMethodProcedureClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID} {
		f := newLockFixture(t)
		a, b, c := f.tx(), f.tx(), f.tx()
		ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		key := ids.NameKey{ClassID: class, Namespace: ns, Name: "definition", Signature: "403"}
		if class == ids.AccessMethodClassID {
			key.Namespace = ids.ObjectAddress{}
			key.Signature = ""
		}
		if class == ids.TSTemplateClassID || class == ids.TSDictionaryClassID {
			key.Signature = ""
		}
		if class == ids.AccessMethodOperatorClassID || class == ids.AccessMethodProcedureClassID {
			key.Namespace = ids.ObjectAddress{}
			key.Signature = ""
			key.Name = "90001,23,23,1,0"
		}
		resource := Resource{ObjectRef: f.domain.Ref(f.epoch, key.Namespace), Name: key}
		if r, err := f.m.StartAcquire(context.Background(), a, resource, AccessExclusive, Policy{}); err != nil || r != nil {
			t.Fatalf("definition class %d resource admission: %v %v", class, r, err)
		}
		mark := f.m.Checkpoint(a)
		other := resource
		other.Name.Name += "_other"
		if r, err := f.m.StartAcquire(context.Background(), a, other, AccessExclusive, Policy{}); err != nil || r != nil {
			t.Fatal(r, err)
		}
		queued, err := f.m.StartAcquire(context.Background(), b, other, AccessExclusive, Policy{})
		if err != nil || queued == nil {
			t.Fatal("same exact definition key did not wait", queued, err)
		}
		f.m.Restore(a, mark)
		if err := f.result(queued); err != nil {
			t.Fatal(err)
		}
		if f.m.Inspect(a).Resources[resource].Holders[a].Mode != AccessExclusive {
			t.Fatal("savepoint released premark name")
		}
		if class == ids.OperatorFamilyClassID || class == ids.OperatorClassCatalogID {
			different := resource
			different.Name.Signature = "405"
			if r, err := f.m.StartAcquire(context.Background(), c, different, AccessExclusive, Policy{}); err != nil || r != nil {
				t.Fatal("different SQL method falsely serialized", r, err)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		pending, err := f.m.StartAcquire(ctx, b, resource, AccessExclusive, Policy{})
		if err != nil || pending == nil {
			t.Fatal(pending, err)
		}
		cancel()
		if err := f.result(pending); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if pids, err := f.m.BlockingPids(f.domain, b.Session().ID()); err != nil || len(pids) != 0 {
			t.Fatal("canceled definition request retained", pids, err)
		}
		pending, err = f.m.StartAcquire(context.Background(), b, resource, AccessExclusive, Policy{})
		if err != nil || pending == nil {
			t.Fatal(pending, err)
		}
		f.m.Fence(f.domain, f.epoch+1)
		if err := f.result(pending); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("replacement did not fence definition waiter", err)
		}
		if got := f.m.Inspect(); len(got.Resources) != 0 || len(got.Owners) != 0 {
			t.Fatal("definition replacement retained tickets", got)
		}
	}
}
