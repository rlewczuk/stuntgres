package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

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
