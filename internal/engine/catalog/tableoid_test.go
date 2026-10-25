package catalog

import (
	"errors"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestCatalogTableOIDSubobjectValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     RelationKind
		view     bool
		eligible bool
	}{
		{"table", TableRelation, false, true}, {"partition", PartitionedRelation, false, true}, {"materialized", MaterializedViewRelation, false, true}, {"sequence", SequenceRelation, false, true}, {"foreign", ForeignRelation, false, true}, {"catalog", CatalogRelation, false, true}, {"summary", CatalogRelation, true, false}, {"view", ViewRelation, false, false}, {"composite", CompositeRelation, false, false}, {"index", IndexRelation, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := storeFixture(16384)
			r := &Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, RelationKind: tc.kind, CatalogView: tc.view, Attributes: []int32{1, 3}, ColumnMeta: ColumnMetadata{Dropped: map[int32]AttributeMetadata{2: {}}}}
			store.State.SetDescriptor(r.Address(), r)
			view := store.Current()
			for _, sub := range []int32{-7, -6, -1, 1, 2, 3, 4} {
				address := r.Address()
				address.SubID = sub
				err := store.ValidateObjectRefLocked(view, store.Domain.Ref(view.Epoch(), address))
				want := sub == 1 || sub == 3 || sub == -6 && tc.eligible
				if (err == nil) != want {
					t.Fatalf("attribute %d: %v eligible=%t", sub, err, want)
				}
				if sub < 0 && r.HasAttribute(sub) {
					t.Fatal("ordinary attribute authority widened")
				}
			}
			address := r.Address()
			address.SubID = -6
			for _, ref := range []ids.ObjectRef{{Address: address, Epoch: view.Epoch()}, new(ids.Domain).Ref(view.Epoch(), address), store.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: address.ObjectID, SubID: -6}), store.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001, SubID: -6})} {
				if err := store.ValidateObjectRefLocked(view, ref); err == nil {
					t.Fatal("invalid supported-negative authority admitted", ref)
				}
			}
			if err := store.ValidateObjectRefLocked(view, store.Domain.Ref(view.Epoch()+1, address)); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("epoch fence", err)
			}
		})
	}
}
