package extension_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
)

func TestDictionaryDependencyDescriptions(t *testing.T) {
	e, _, installation := extensionUnitInstalled(t)
	view := e.CatalogRead()
	for _, object := range []struct {
		member int
		kind   string
	}{{4, "text search template "}, {5, "text search dictionary "}} {
		address := installation.Members[object.member]
		for _, tc := range []struct {
			path []ids.ObjectAddress
			name string
		}{
			{nil, "unaccent"},
			{[]ids.ObjectAddress{installation.TargetNamespace}, "unaccent"},
			{[]ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}, "public.unaccent"},
		} {
			if got := stored.DependencyObject(view, address, tc.path); got != object.kind+tc.name {
				t.Fatalf("text-search dependency %v on path %v: got %q, want %q", address, tc.path, got, object.kind+tc.name)
			}
		}
	}
}

func extensionUnitInstalled(t *testing.T) (*core.Engine, *session.Operation, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION unaccent")
	a := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
	return e, op, e.Catalog.State.MustExtension(a)
}
