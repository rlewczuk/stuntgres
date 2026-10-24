package lock

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

func TestTransactionalRoutineNameResources(t *testing.T) {
	t.Run("procedure-provider-lifetime", func(t *testing.T) {
		f := newLockFixture(t)
		reader, writer := f.tx(), f.tx()
		provider := Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 99001})}
		f.request(reader, provider, AccessShare)
		mark := f.m.Checkpoint(reader)
		another := provider
		another.Address.ObjectID++
		f.request(reader, another, AccessShare)
		waiting := f.request(writer, another, AccessExclusive)
		if waiting == nil {
			t.Fatal("retirement bypassed provider reader")
		}
		records, err := f.m.PGLocks(f.domain, f.epoch)
		if err != nil {
			t.Fatal(err)
		}
		objects := 0
		for _, record := range records {
			if record.Kind == RelationRecord {
				t.Fatal("procedure fabricated a relation tag", record)
			}
			if record.Kind == ObjectRecord && record.Class == uint32(ids.ProcedureClassID) {
				objects++
			}
		}
		if objects != 3 {
			t.Fatal("procedure holder/waiter object projection", objects)
		}
		f.m.Restore(reader, mark)
		if err := f.result(waiting); err != nil {
			t.Fatal(err)
		}
		waiting, err = f.m.StartAcquire(context.Background(), writer, provider, AccessExclusive, Policy{Timeout: time.Hour})
		if err != nil || waiting == nil {
			t.Fatal("provider did not retain premark hold", err)
		}
		f.diagnostic(f.m.ExpireWait(waiting, waiting.deadline), "55P03", "canceling statement due to lock timeout")
		f.diagnostic(f.result(waiting), "55P03", "canceling statement due to lock timeout")
		waiting = f.request(writer, provider, AccessExclusive)
		if waiting == nil {
			t.Fatal("provider timeout released reader")
		}
		f.m.End(reader)
		if err := f.result(waiting); err != nil {
			t.Fatal(err)
		}
		f.m.End(writer)
		f.empty()
	})
	for _, class := range []ids.OID{ids.ProcedureClassID, ids.OperatorClassID} {
		signatures := []string{"", "23", "23,23", "90001"}
		if class == ids.OperatorClassID {
			signatures = []string{"23,23", "0,23"}
		}
		for _, signature := range signatures {
			t.Run(fmt.Sprintf("%d/%s", class, signature), func(t *testing.T) {
				f := newLockFixture(t)
				a, b := f.tx(), f.tx()
				key := transactionalNameResource(f, "overloaded")
				key.Name.ClassID, key.Name.Signature = class, signature
				if f.request(a, key, AccessExclusive) != nil {
					t.Fatal("uncontended full key queued")
				}
				other := key
				other.Name.Signature += ",20"
				if signature == "" {
					other.Name.Signature = "20"
				}
				if class == ids.OperatorClassID {
					other.Name.Signature = "20,23"
				}
				if f.request(b, other, AccessExclusive) != nil {
					t.Fatal("distinct signature serialized")
				}
				other = key
				other.Name.ClassID = ids.RelationClassID
				other.Name.Signature = ""
				if f.request(b, other, AccessExclusive) != nil {
					t.Fatal("distinct class serialized")
				}
				mark := f.m.Checkpoint(a)
				late := key
				late.Name.Name = "late"
				f.request(a, late, AccessExclusive)
				waiting := f.request(b, late, AccessExclusive)
				if waiting == nil {
					t.Fatal("exact signature bypassed holder")
				}
				f.m.Restore(a, mark)
				if err := f.result(waiting); err != nil {
					t.Fatal(err)
				}
				if f.m.Inspect(a).Resources[key].Holders[a].Mode != AccessExclusive {
					t.Fatal("premark key lost")
				}
				ctx, cancel := context.WithCancel(context.Background())
				waiting, err := f.m.StartAcquire(ctx, b, key, AccessExclusive, Policy{})
				if err != nil || waiting == nil {
					t.Fatal("full key did not wait", err)
				}
				cancel()
				if err := f.result(waiting); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				waiting = f.request(b, key, AccessExclusive)
				if waiting == nil {
					t.Fatal("cancellation released holder")
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
	t.Run("provenance-and-replacement", func(t *testing.T) {
		f := newLockFixture(t)
		a, b := f.tx(), f.tx()
		key := transactionalNameResource(f, "zero_arity")
		key.Name.ClassID = ids.ProcedureClassID
		for _, bad := range []Resource{
			{ObjectRef: new(ids.Domain).Ref(f.epoch, key.Address), Name: key.Name},
			{ObjectRef: f.domain.Ref(f.epoch+1, key.Address), Name: key.Name},
			{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1}), Name: key.Name},
		} {
			if r, err := f.m.StartAcquire(context.Background(), a, bad, AccessExclusive, Policy{}); err == nil || r != nil {
				t.Fatal("invalid resource", bad, err)
			}
		}
		for _, class := range []ids.OID{ids.NamespaceClassID, ids.RelationClassID, ids.TypeClassID} {
			bad := key
			bad.Name.ClassID, bad.Name.Signature = class, "23"
			if r, err := f.m.StartAcquire(context.Background(), a, bad, AccessExclusive, Policy{}); err == nil || r != nil {
				t.Fatal("non-overloaded signature admitted", class, err)
			}
		}
		f.request(a, key, AccessExclusive)
		waiting := f.request(b, key, AccessExclusive)
		if waiting == nil {
			t.Fatal("missing waiter")
		}
		f.m.Fence(f.domain, f.epoch+1)
		if err := f.result(waiting); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal(err)
		}
		if snapshot := f.m.Inspect(); len(snapshot.Resources) != 0 || len(snapshot.Owners) != 0 {
			t.Fatal("replacement retained signature state", snapshot)
		}
	})
}
