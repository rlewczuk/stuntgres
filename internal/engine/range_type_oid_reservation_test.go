package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRangeTypeReplacementOIDFailureRecovery(t *testing.T) {
	ctx := context.Background()
	view, r, _ := rangeTypeUnitFamily(t, sqltype.Regclass, false)
	next := catalog.CloneState(view.State())
	lower := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 30000}, ObjectRevision: 1, ObjectName: "lower", RelationKind: catalog.TableRelation}
	ordinary := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 30001}, ObjectRevision: 1, ObjectName: "ordinary"}
	next.SetDescriptor(lower.Address(), lower)
	next.SetDescriptor(ordinary.Address(), ordinary)
	target := New()
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDAtLeastLocked(29999)
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	op, done := roleOperation(t, target, "postgres")
	defer done()
	roleExecute(t, target, op, "CREATE TEMP TABLE reservation_live(k integer)")
	roleExecute(t, target, op, "INSERT INTO reservation_live VALUES(7)")
	retained, err := control.TemporarySet(target.CatalogRead(), next)
	if err != nil {
		t.Fatal(err)
	}
	before := target.Catalog.State
	target.Mu.Lock()
	base := target.Catalog.NextOID()
	target.Mu.Unlock()
	for _, input := range []string{"[30000,30002]", "[30000,4294967295]"} {
		value := rangeTypeUnitInput(t, r, input)
		constraints, err := stored.RangeIdentityConstraints(ctx, next, map[ids.ObjectAddress]*storage.Relation{lower.Address(): {Versions: []storage.RowVersion{{Values: []sqltype.Value{value}}}}})
		if err != nil {
			t.Fatal(err)
		}
		target.Mu.Lock()
		reservation := target.Catalog.OIDReservationLocked()
		mapping, err := control.TemporaryCollisionMapping(ctx, next, retained, constraints, func(floor ids.OID) (ids.OID, error) {
			return target.Catalog.AllocateOIDFromLocked(&reservation.Next, floor)
		})
		live := target.Catalog.NextOID()
		target.Mu.Unlock()
		if live != base || target.Catalog.State != before {
			t.Fatal("unpublished planning issued identities or changed target", live, base)
		}
		if input == "[30000,30002]" {
			var diagnostic interface{ SQLState() string }
			if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "22000" || mapping != nil || reservation.Next != 0 {
				t.Fatal("negative proof did not exercise private exhaustion", mapping, reservation, err)
			}
		} else if err != nil || mapping[lower.Address()].ObjectID != base || mapping[ordinary.Address()].ObjectID != base+1 || reservation.Next != base+2 {
			t.Fatal("same target could not plan a corrected replacement after failure", mapping, reservation, err)
		}
	}
	if data := tuples(roleExecute(t, target, op, "SELECT k FROM reservation_live")); len(data) != 1 || data[0][0] != int64(7) {
		t.Fatal("failed reservation changed retained data", data)
	}
	target.Mu.Lock()
	fresh, err := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil || fresh != base {
		t.Fatal("discarded private reservation poisoned ordinary DDL capacity", fresh, base, err)
	}
}

func TestRangeTypeReplacementOIDPublicationFence(t *testing.T) {
	for _, mode := range []string{"commit", "cancel", "ordinary_allocation"} {
		t.Run(mode, func(t *testing.T) {
			target := New()
			target.ReplacementMu.Lock()
			defer target.ReplacementMu.Unlock()
			target.Mu.Lock()
			defer target.Mu.Unlock()
			before := target.Catalog.State
			reservation := target.Catalog.OIDReservationLocked()
			if _, err := target.Catalog.AllocateOIDFromLocked(&reservation.Next, 0); err != nil {
				t.Fatal(err)
			}
			if target.Catalog.NextOID() != reservation.Base {
				t.Fatal("private reservation became live before installation")
			}
			candidate := catalog.CloneState(before)
			relations := map[ids.ObjectAddress]*storage.Relation{}
			sequences := map[ids.ObjectAddress]*storage.SequenceGenerator{}
			prepared, err := EnginePrepareForeignIndexStorage(target, context.Background(), candidate, relations, &index.Snapshot{})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected := reservation.Base
			switch mode {
			case "cancel":
				cancel()
			case "ordinary_allocation":
				if _, err := target.Catalog.AllocateOIDLocked(); err != nil {
					t.Fatal(err)
				}
				expected = target.Catalog.NextOID()
			}
			err = EngineInstallForeignCatalogLocked(target, ctx, candidate, relations, sequences, target.Catalog.NextRevision(), prepared, reservation)
			if mode == "commit" {
				if err != nil || target.Catalog.State == before || target.Catalog.State.Epoch() != before.Epoch()+1 || target.Catalog.NextOID() != reservation.Next {
					t.Fatal("catalog and reserved frontier did not publish together", err)
				}
			} else {
				want := error(context.Canceled)
				if mode == "ordinary_allocation" {
					want = ids.ErrCatalogReplaced
				}
				if !errors.Is(err, want) || target.Catalog.State != before || target.Catalog.NextOID() != expected {
					t.Fatal("failed candidate published or erased concurrent ordinary issuance", mode, err, target.Catalog.NextOID(), expected)
				}
			}
		})
	}
}
