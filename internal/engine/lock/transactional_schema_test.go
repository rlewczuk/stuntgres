package lock

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func transactionalNameResource(f *lockFixture, name string) Resource {
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	return Resource{ObjectRef: f.domain.Ref(f.epoch, ns), Name: ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name}}
}

func TestTransactionalSchemaLockResources(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		f := newLockFixture(t)
		a := f.tx()
		name := transactionalNameResource(f, "table_name")
		for _, bad := range []struct {
			resource Resource
			mode     Mode
		}{
			{Resource{ObjectRef: new(ids.Domain).Ref(f.epoch, name.Address), Name: name.Name}, AccessExclusive},
			{Resource{ObjectRef: f.domain.Ref(f.epoch+1, name.Address), Name: name.Name}, AccessExclusive},
			{Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{}), Name: name.Name}, AccessExclusive},
			{Resource{ObjectRef: name.ObjectRef, Name: ids.NameKey{ClassID: ids.TypeClassID, Namespace: name.Address}}, AccessExclusive},
			{Resource{ObjectRef: name.ObjectRef, Name: name.Name, Row: 1}, AccessExclusive},
			{name, AccessShare},
			{Resource{ObjectRef: name.ObjectRef}, TypeExclusive},
		} {
			if r, err := f.m.StartAcquire(context.Background(), a, bad.resource, bad.mode, Policy{}); err == nil || r != nil {
				t.Fatal("invalid resource admitted", bad, r, err)
			}
		}
		schemaName := Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: ids.NamespaceClassID, Name: "schema_name"}}
		if f.request(a, schemaName, AccessExclusive) != nil {
			t.Fatal("schema binding queued without a holder")
		}
	})
	t.Run("checkpoint_cancel_projection", func(t *testing.T) {
		f := newLockFixture(t)
		a, b := f.tx(), f.tx()
		early, late, other := transactionalNameResource(f, "early"), transactionalNameResource(f, "late"), transactionalNameResource(f, "other")
		f.request(a, early, AccessExclusive)
		mark := f.m.Checkpoint(a)
		f.request(a, late, AccessExclusive)
		if f.request(b, other, AccessExclusive) != nil {
			t.Fatal("unrelated key serialized")
		}
		waiting := f.request(b, late, AccessExclusive)
		if waiting == nil {
			t.Fatal("collision did not queue")
		}
		pids, err := f.m.BlockingPids(f.domain, b.Session().ID())
		if err != nil || len(pids) != 1 || pids[0] != int32(a.Session().ID()) {
			t.Fatal(pids, err)
		}
		records, err := f.m.PGLocks(f.domain, f.epoch)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range records {
			if r.Kind == RelationRecord || r.Kind == ObjectRecord {
				t.Fatal("catalog name fabricated a physical tag", r)
			}
		}
		f.m.Restore(a, mark)
		if err := f.result(waiting); err != nil {
			t.Fatal(err)
		}
		if f.m.Inspect(a).Resources[early].Holders[a].Mode != AccessExclusive {
			t.Fatal("premark binding lost")
		}
		ctx, cancel := context.WithCancel(context.Background())
		r, err := f.m.StartAcquire(ctx, b, early, AccessExclusive, Policy{})
		if err != nil || r == nil {
			t.Fatal(r, err)
		}
		cancel()
		if err := f.result(r); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if pids, err := f.m.BlockingPids(f.domain, b.Session().ID()); err != nil || len(pids) != 0 {
			t.Fatal("canceled request remains", pids, err)
		}
		f.m.End(a)
		f.m.End(b)
		f.empty()
	})
	t.Run("namespace_lifetime_and_fence", func(t *testing.T) {
		f := newLockFixture(t)
		a, b := f.tx(), f.tx()
		ns := Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 19000})}
		f.request(a, ns, AccessShare)
		r := f.request(b, ns, AccessExclusive)
		if r == nil {
			t.Fatal("namespace drop bypassed existence hold")
		}
		records, err := f.m.PGLocks(f.domain, f.epoch)
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, r := range records {
			if r.Kind == ObjectRecord && r.Class == uint32(ids.NamespaceClassID) && r.Object == 19000 {
				seen++
			}
		}
		if seen != 2 {
			t.Fatal("namespace physical object projection", records)
		}
		f.m.End(a)
		if err := f.result(r); err != nil {
			t.Fatal(err)
		}
		name := transactionalNameResource(f, "fenced")
		f.request(b, name, AccessExclusive)
		c := f.tx()
		queued := f.request(c, name, AccessExclusive)
		if queued == nil {
			t.Fatal("missing name waiter")
		}
		f.m.Fence(f.domain, f.epoch+1)
		if err := f.result(queued); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal(err)
		}
		if snapshot := f.m.Inspect(); len(snapshot.Resources) != 0 || len(snapshot.Owners) != 0 {
			t.Fatal("fence retained name/namespace state", snapshot)
		}
	})
	t.Run("name_deadlock", func(t *testing.T) {
		f := newLockFixture(t)
		a, b := f.tx(), f.tx()
		x, y := transactionalNameResource(f, "x"), transactionalNameResource(f, "y")
		f.request(a, x, AccessExclusive)
		f.request(b, y, AccessExclusive)
		first := f.request(a, y, AccessExclusive)
		victim := f.request(b, x, AccessExclusive)
		var diagnostic *sqlstate.Error
		if err := f.result(victim); !errors.As(err, &diagnostic) || diagnostic.Code != "40P01" || !strings.Contains(diagnostic.Detail, "catalog name") || strings.Contains(diagnostic.Detail, "advisory") {
			t.Fatal(err)
		}
		f.m.End(b)
		if err := f.result(first); err != nil {
			t.Fatal(err)
		}
		f.m.End(a)
		f.empty()
	})
}
