package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/engine/session"
)

func TestLargeObjectSQLRoutineScratch(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 910091}
	content, err := (largeobject.Value{}).Write(0, "aa")
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	base := catalog.CloneState(e.Catalog.State)
	object := &catalog.LargeObject{ObjectAddress: address, ObjectName: "910091", ObjectRevision: 2, Owner: e.RoleStore.Domain.Principal(10), Content: content}
	base.SetDescriptor(address, object)
	base.BindName(catalog.DescriptorNameKey(object), address)
	e.Catalog.PublishLocked(base)
	outerRead := e.Catalog.Read(base)
	e.Mu.Unlock()

	bytesIn := func(view catalog.Read) string {
		t.Helper()
		d, ok := view.Get(address).(*catalog.LargeObject)
		if !ok {
			t.Fatal("missing object in query capture")
		}
		got, err := d.Content.Read(0, -1)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, primary := range []error{nil, errors.New("body failure"), context.Canceled} {
		owner := &session.TransactionOwner{Catalog: session.NewJournal()}
		ownerJournal := owner.Catalog
		candidate := ownerJournal.Clone()
		bodyObject := *object
		bodyObject.Content, err = object.Content.Write(0, "bb")
		if err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		e.CatalogChangeLocked(candidate, address, &bodyObject)
		bodyRead := e.PrivateCatalogLocked(candidate)
		e.Mu.Unlock()
		scope := core.NewScope(core.ScopeFields{Owner: owner, Catalog: candidate, LargeObjectRead: outerRead})
		restoreOuter := scope.ActivateSQLRoutine(nil, bodyRead)
		if got := bytesIn(core.ScopeLargeObjectRead(scope)); got != "bb" {
			t.Fatalf("body read %q, want calling-command bytes bb", got)
		}

		// Another body effect stays in the same journal. Only a nested body
		// query sees it; the active body's immutable READ view remains bb.
		nestedObject := bodyObject
		nestedObject.Content, err = bodyObject.Content.Write(0, "cc")
		if err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		e.CatalogChangeLocked(candidate, address, &nestedObject)
		nestedRead := e.PrivateCatalogLocked(candidate)
		e.Mu.Unlock()
		if got := bytesIn(core.ScopeLargeObjectRead(scope)); got != "bb" {
			t.Fatalf("active body saw later effect %q", got)
		}
		restoreBody := scope.ActivateSQLRoutine(nil, nestedRead)
		if got := bytesIn(core.ScopeLargeObjectRead(scope)); got != "cc" {
			t.Fatalf("nested query read %q, want cc", got)
		}
		if err := restoreBody(primary); !errors.Is(err, primary) {
			t.Fatal("nested unwind changed primary error", err)
		}
		if got := bytesIn(core.ScopeLargeObjectRead(scope)); got != "bb" {
			t.Fatalf("nested unwind restored %q, want body bytes bb", got)
		}
		if err := restoreOuter(primary); !errors.Is(err, primary) {
			t.Fatal("outer unwind changed primary error", err)
		}
		if got := bytesIn(core.ScopeLargeObjectRead(scope)); got != "aa" {
			t.Fatalf("outer unwind restored %q, want original bytes aa", got)
		}
		if scope.Owner != owner || owner.Catalog != ownerJournal || scope.Catalog != candidate || len(ownerJournal.Descriptors) != 0 {
			t.Fatal("query activation replaced owner or effect journal")
		}
		e.Mu.Lock()
		commandRead := e.PrivateCatalogLocked(scope.Catalog)
		e.Mu.Unlock()
		if got := bytesIn(commandRead); got != "cc" {
			t.Fatalf("unwind discarded shared command effect %q", got)
		}
		if got := bytesIn(outerRead); got != "aa" {
			t.Fatalf("immutable outer capture changed to %q", got)
		}
	}
}
