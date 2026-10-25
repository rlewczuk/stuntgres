package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestRangeTypeReplacementOIDFailureRecovery(t *testing.T) {
	ctx := context.Background()
	view, r, _ := rangeTypeUnitFamily(t, sqltype.Regclass, false)
	next := catalog.CloneState(view.State())
	lower := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 30000}, ObjectRevision: 1, ObjectName: "lower", RelationKind: catalog.TableRelation}
	ordinary := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 30001}, ObjectRevision: 1, ObjectName: "ordinary"}
	next.SetDescriptor(lower.Address(), lower)
	next.SetDescriptor(ordinary.Address(), ordinary)
	target := engine.New()
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDAtLeastLocked(29999)
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	op, done := enginetest.RoleOperation(t, target, "postgres")
	defer done()
	enginetest.RoleExecute(t, target, op, "CREATE TEMP TABLE reservation_live(k integer)")
	enginetest.RoleExecute(t, target, op, "INSERT INTO reservation_live VALUES(7)")
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
	if data := enginetest.Tuples(enginetest.RoleExecute(t, target, op, "SELECT k FROM reservation_live")); len(data) != 1 || data[0][0] != int64(7) {
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
			target := engine.New()
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
			prepared, err := core.EnginePrepareForeignIndexStorage(target, context.Background(), candidate, relations, &index.Snapshot{})
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
			err = core.EngineInstallForeignCatalogLocked(target, ctx, candidate, relations, sequences, target.Catalog.NextRevision(), prepared, reservation)
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

// Real catalog classes, members and executable declarations supply every capture.
// Changing the candidate member is not a fabricated WithSupport callback.
func rangeTypeUnitFamily(t *testing.T, subtype sqltype.ID, subtract bool) (catalog.Read, *sqltype.Descriptor, *sqltype.Descriptor) {
	t.Helper()
	e := engine.New()
	state := catalog.CloneState(e.CatalogRead().State())
	view := catalog.NewRead(e.CatalogRead().Owner(), state)
	u, _ := view.LookupType(subtype)
	class, ok, defaultErr := view.DefaultIndexClass(403, u)
	if defaultErr != nil {
		t.Fatal(defaultErr)
	}
	if !ok && (u.ContainsRegclass() || u.ContainsRegtype()) {
		oid, _ := view.LookupType(sqltype.Oid)
		class, ok, defaultErr = view.DefaultIndexClass(403, oid)
		if defaultErr != nil {
			t.Fatal(defaultErr)
		}
	}
	if !ok {
		t.Fatal("missing actual subtype class")
	}
	if subtract {
		member, ok := view.FamilySupport(ids.OID(class.FamilyOID()), 23, 23, 1)
		if !ok {
			t.Fatal("missing actual support member")
		}
		copy := *member
		copy.Function = 181
		copy.ObjectRevision++
		state.SetDescriptor(copy.Address(), &copy)
	}
	class, err := routine.BindClassSupport(view, class)
	if err != nil {
		t.Fatal(err)
	}
	ordering, err := routine.BindIndexOrdering(view, u.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	boolean, _ := view.LookupType(sqltype.Bool)
	r, m, err := sqltype.NewRangeFamily(900001, "unit_span", "public.unit_span", 900002, "unit_spans", "public.unit_spans", sqltype.RangeDefinition{Subtype: u.Usage(), Class: class, Ordering: ordering}, boolean)
	if err != nil {
		t.Fatal(err)
	}
	owner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(subtype)}).Owner
	for i, typ := range []*sqltype.Descriptor{r, m} {
		array, err := sqltype.Array(sqltype.ID(900003+i), "_"+typ.Name(), typ)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range []*catalog.Type{{SQLType: typ, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 2, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true, Array: array.ID()}, {SQLType: array, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 2, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true}} {
			state.SetDescriptor(d.Address(), d)
			state.BindName(catalog.DescriptorNameKey(d), d.Address())
		}
	}
	return view, r, m
}

func rangeTypeUnitInput(t *testing.T, d *sqltype.Descriptor, text string) sqltype.Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(text, err)
	}
	return v
}
