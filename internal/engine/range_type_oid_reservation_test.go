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
	target.mu.Lock()
	_, err := target.catalog.AllocateOIDAtLeastLocked(29999)
	target.mu.Unlock()
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
	before := target.catalog.State
	target.mu.Lock()
	base := target.catalog.NextOID()
	target.mu.Unlock()
	for _, input := range []string{"[30000,30002]", "[30000,4294967295]"} {
		value := rangeTypeUnitInput(t, r, input)
		constraints, err := stored.RangeIdentityConstraints(ctx, next, map[ids.ObjectAddress]*storage.Relation{lower.Address(): {Versions: []storage.RowVersion{{Values: []sqltype.Value{value}}}}})
		if err != nil {
			t.Fatal(err)
		}
		target.mu.Lock()
		reservation := target.catalog.OIDReservationLocked()
		mapping, err := control.TemporaryCollisionMapping(ctx, next, retained, constraints, func(floor ids.OID) (ids.OID, error) {
			return target.catalog.AllocateOIDFromLocked(&reservation.Next, floor)
		})
		live := target.catalog.NextOID()
		target.mu.Unlock()
		if live != base || target.catalog.State != before {
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
	target.mu.Lock()
	fresh, err := target.catalog.AllocateOIDLocked()
	target.mu.Unlock()
	if err != nil || fresh != base {
		t.Fatal("discarded private reservation poisoned ordinary DDL capacity", fresh, base, err)
	}
}

func TestRangeTypeReplacementOIDPublicationFence(t *testing.T) {
	for _, mode := range []string{"commit", "cancel", "ordinary_allocation"} {
		t.Run(mode, func(t *testing.T) {
			target := New()
			target.replacementMu.Lock()
			defer target.replacementMu.Unlock()
			target.mu.Lock()
			defer target.mu.Unlock()
			before := target.catalog.State
			reservation := target.catalog.OIDReservationLocked()
			if _, err := target.catalog.AllocateOIDFromLocked(&reservation.Next, 0); err != nil {
				t.Fatal(err)
			}
			if target.catalog.NextOID() != reservation.Base {
				t.Fatal("private reservation became live before installation")
			}
			candidate := catalog.CloneState(before)
			relations := map[ids.ObjectAddress]*storage.Relation{}
			sequences := map[ids.ObjectAddress]*storage.SequenceGenerator{}
			prepared, err := target.prepareForeignIndexStorage(context.Background(), candidate, relations, &index.Snapshot{})
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
				if _, err := target.catalog.AllocateOIDLocked(); err != nil {
					t.Fatal(err)
				}
				expected = target.catalog.NextOID()
			}
			err = target.installForeignCatalogLocked(ctx, candidate, relations, sequences, target.catalog.NextRevision(), prepared, reservation)
			if mode == "commit" {
				if err != nil || target.catalog.State == before || target.catalog.State.Epoch() != before.Epoch()+1 || target.catalog.NextOID() != reservation.Next {
					t.Fatal("catalog and reserved frontier did not publish together", err)
				}
			} else {
				want := error(context.Canceled)
				if mode == "ordinary_allocation" {
					want = ids.ErrCatalogReplaced
				}
				if !errors.Is(err, want) || target.catalog.State != before || target.catalog.NextOID() != expected {
					t.Fatal("failed candidate published or erased concurrent ordinary issuance", mode, err, target.catalog.NextOID(), expected)
				}
			}
		})
	}
}
