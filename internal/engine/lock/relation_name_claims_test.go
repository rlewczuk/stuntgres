package lock

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestRelationNameClaimsSnapshot(t *testing.T) {
	f := newLockFixture(t)
	a, b := f.tx(), f.tx()
	own := transactionalNameResource(f, "own")
	early := transactionalNameResource(f, "early")
	late := transactionalNameResource(f, "late")
	f.request(a, own, AccessExclusive)
	f.request(b, early, AccessExclusive)
	mark := f.m.Checkpoint(b)
	f.request(b, late, AccessExclusive)
	// A request queued behind the requesting owner's own name is not an
	// independently visible reservation; only granted other-owner names count.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pending, err := f.m.StartAcquire(ctx, b, own, AccessExclusive, Policy{})
	if err != nil || pending == nil {
		t.Fatal("name conflict did not queue", err)
	}
	typ := transactionalNameResource(f, "type_name")
	typ.Name.ClassID = ids.TypeClassID
	f.request(b, typ, AccessExclusive)
	other := transactionalNameResource(f, "other_namespace")
	other.Address.ObjectID++
	other.Name.Namespace = other.Address
	f.request(b, other, AccessExclusive)
	var scratch [8]ids.NameKey
	claims, err := f.m.AppendRelationNameClaims(scratch[:0], a, own.ObjectRef)
	if err != nil || len(claims) != 2 || !slices.Contains(claims, early.Name) || !slices.Contains(claims, late.Name) {
		t.Fatal("wrong owner/class/namespace filter", claims, err)
	}
	if &claims[0] != &scratch[0] {
		t.Fatal("did not reuse caller scratch")
	}
	retained := slices.Clone(claims)
	cancel()
	if err := f.result(pending); !errors.Is(err, context.Canceled) {
		t.Fatal("pending request did not retire", err)
	}
	f.m.Restore(b, mark)
	if !reflect.DeepEqual(claims, retained) {
		t.Fatal("rollback mutated returned snapshot")
	}
	var afterScratch [8]ids.NameKey
	after, err := f.m.AppendRelationNameClaims(afterScratch[:0], a, own.ObjectRef)
	if err != nil || len(after) != 1 || after[0] != early.Name {
		t.Fatal("postmark claim survived rollback", after, err)
	}
	for _, bad := range []ids.ObjectRef{f.domain.Ref(f.epoch+1, own.Address), new(ids.Domain).Ref(f.epoch, own.Address)} {
		if _, err := f.m.AppendRelationNameClaims(nil, a, bad); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("invalid namespace provenance", err)
		}
	}
	f.m.End(b)
	if !reflect.DeepEqual(claims, retained) {
		t.Fatal("release mutated detached snapshot")
	}
	after, err = f.m.AppendRelationNameClaims(afterScratch[:0], a, own.ObjectRef)
	if err != nil || len(after) != 0 {
		t.Fatal("released owner retained claims", after, err)
	}
	f.m.End(a)
	if _, err := f.m.AppendRelationNameClaims(nil, a, own.ObjectRef); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("ended owner admitted", err)
	}
	f.empty()
}
