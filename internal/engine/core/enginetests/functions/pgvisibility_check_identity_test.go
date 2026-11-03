package functions_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func visibilityCheckPrivateIdentity(t *testing.T) {
	e, ref := visibilityCheckFixture(t, true, false)
	public := enginetest.StartSession(t, e, nil)
	principal := public.Authorization.State.Authenticated
	owners := make([]*session.Session, 2)
	for i := range owners {
		private, err := e.StartRestoreSession(t.Context(), principal)
		if err != nil {
			t.Fatal(err)
		}
		owners[i] = private
		t.Cleanup(func() {
			private.RequestEnd(session.CleanClose)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := private.WaitEnd(ctx); err != nil {
				t.Error(err)
			}
		})
		enginetest.SessionSQL(t, private, "BEGIN")
		enginetest.SessionSQL(t, private, "INSERT INTO visibility_heap VALUES(2)")
	}
	a, b := owners[0].Transactions.Ref, owners[1].Transactions.Ref
	if a.Session().ID() != 0 || b.Session().ID() != 0 || a.ID() != 1 || b.ID() != 1 || a.Diagnostic() == 0 || b.Diagnostic() == 0 || a.Diagnostic() == b.Diagnostic() || a == b {
		t.Fatal("actual private runtime identities collide", a, b)
	}
	store := e.Storage.Relations[ref.Address]
	factsA := []storage.HeapTupleFact{{Version: owners[0].Transactions.Writes[ref.Address][0], Inserter: a}}
	factsB := []storage.HeapTupleFact{{Version: owners[1].Transactions.Writes[ref.Address][0], Inserter: b}}
	basisA, basisB := storage.CaptureVisibilityBasis(store, factsA), storage.CaptureVisibilityBasis(store, factsB)
	keyA, keyB := basisA.Facts[0].Inserter, basisB.Facts[0].Inserter
	if keyA == keyB || keyA.Domain != a.Domain() || keyB.Domain != b.Domain() || keyA.Diagnostic != a.Diagnostic() || keyB.Diagnostic != b.Diagnostic() || !basisA.Same(store, factsA) || !basisB.Same(store, factsB) {
		t.Fatal("persistent private keys lost actual diagnostic identity", keyA, keyB)
	}
	// Inspect actual retained backing, not rendered SQL/source text: every
	// persistent fact member is scalar except the tiny opaque Domain token.
	var detached func(reflect.Type)
	detached = func(typ reflect.Type) {
		if typ == reflect.TypeFor[*ids.Domain]() {
			return
		}
		switch typ.Kind() {
		case reflect.Struct:
			for i := range typ.NumField() {
				detached(typ.Field(i).Type)
			}
		case reflect.Slice, reflect.Array:
			detached(typ.Elem())
		case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Chan, reflect.UnsafePointer:
			t.Fatal("persistent facts retained a live/opaque graph", typ)
		}
	}
	detached(reflect.TypeOf(basisA.Facts))
	// A nonzero ref lacking the real allocator's diagnostic is not an absent
	// owner and may never be reused as an interchangeable persistent identity.
	invalid := factsA[0]
	invalid.Inserter = a.Domain().Tx(a.Session(), a.ID(), a.Epoch(), a.Attachment(), 0)
	badFacts := []storage.HeapTupleFact{invalid}
	bad := storage.CaptureVisibilityBasis(store, badFacts)
	if bad.Same(store, badFacts) || !bad.Facts[0].Inserter.Present {
		t.Fatal("invalid diagnostic-zero ref acquired reusable identity")
	}
	for _, owner := range owners {
		enginetest.SessionSQL(t, owner, "ROLLBACK")
	}
}

func visibilityCheckCatalogMap(t *testing.T) {
	e, _ := visibilityCheckFixture(t, true, false)
	view := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "pg_catalog.pg_class")
	target := view.MustRelation(ref.Address)
	if e.Storage.Relations[ref.Address] != nil {
		t.Fatal("producer catalog unexpectedly has a physical heap")
	}
	scope, finish := visibilityScope(t, e, enginetest.StartSession(t, e, nil))
	defer finish(nil)
	truncate := visibilityCheckNode(t, e, "pg_truncate_visibility_map")
	if err := core.HostOf(scope).Contrib().PgvisibilityTruncate(truncate, uint32(ref.Address.ObjectID)); err != nil {
		t.Fatal(err)
	}
	metadata := e.Storage.RelationMetadata[ref.Address]
	if metadata == nil || metadata.VisibilityTarget != target || metadata.Visibility == nil || !metadata.Visibility.Invalidated {
		t.Fatal("catalog map lacks exact descriptor-anchored metadata")
	}
	read := visibilityCheckNode(t, e, "pg_visibility_map_summary")
	result, err := core.HostOf(scope).Contrib().Pgvisibility(read, uint32(ref.Address.ObjectID), 0)
	if err != nil || result.AllVisible != 0 || result.AllFrozen != 0 || result.MappedBlocks != 0 {
		t.Fatal("producer map inquiry", result, err)
	}
	if e.Storage.Relations[ref.Address] != nil || e.Storage.RelationMetadata[ref.Address] != metadata {
		t.Fatal("producer inquiry invented heap data or republished map")
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
}
