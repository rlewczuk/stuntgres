package enginetest

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CatalogAuthorization is the authorization view of the bootstrap principal
// over view, as host catalog fixtures resolve. The caller holds e.Mu.
func CatalogAuthorization(e *core.Engine, view catalog.Read) core.AuthorizationView {
	return e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: view, RoleRecord: e.RoleStore.Published, Catalog: view, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, view), State: authorization.RoleState{CurrentUser: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}})
}

// CatalogRef resolves the relation public.name in view as the bootstrap
// principal.
func CatalogRef(t testing.TB, e *core.Engine, view catalog.Read, name string) ids.ObjectRef {
	t.Helper()
	e.Mu.Lock()
	ref, err := e.ResolveReference(CatalogAuthorization(e, view), parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: name}}})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// RoleTableRef resolves the table name, as written in a FROM clause, in the
// current catalog as the bootstrap principal.
func RoleTableRef(t testing.TB, e *core.Engine, name string) ids.ObjectRef {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	ref, err := e.ResolveReference(CatalogAuthorization(e, e.Catalog.Current()), Statement(t, "SELECT * FROM "+name).(*parser.Select).From.(*parser.TableReference).Name)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// PublishCatalog publishes p outside any operation or statement.
func PublishCatalog(e *core.Engine, p *core.PreparedCatalogPublication, token core.PublicationToken) (core.ChangeSet, error) {
	return e.PublishPreparedCatalog(nil, nil, p, token)
}

// ValidateObjectRef checks, under e.Mu, that view and ref belong to e.
func ValidateObjectRef(e *core.Engine, view catalog.Read, ref ids.ObjectRef) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.Catalog.ValidateObjectRefLocked(view, ref)
}

// RelationLocatorReplacement requires an immutable current clone whose only
// change from expected is a fresh, nonzero logical storage locator. It does not
// relax defining metadata, revisions, ACLs, providers or retained-root ownership.
func RelationLocatorReplacement(t testing.TB, view catalog.Read, expected *catalog.Relation) *catalog.Relation {
	t.Helper()
	current := view.MustRelation(expected.Address())
	if current == expected || current.StorageLocator == 0 || current.StorageLocator == expected.EffectiveStorageLocator(view) {
		t.Fatal("replacement did not publish a fresh descriptor/locator", expected.Address())
	}
	want := *expected
	want.StorageLocator = current.StorageLocator
	if !catalog.RelationDefiningAuthorityCompatible(expected, current) || current.SQLIndex != expected.SQLIndex || !reflect.DeepEqual(current, &want) {
		t.Fatal("locator replacement changed unrelated descriptor authority", expected.Address())
	}
	return current
}

// ReindexRelationReplacement checks StageMaintenance's exact REINDEX transition:
// revision+1, an owned ready/valid/live definition clone and a fresh locator.
// All key/constraint/predicate providers and remaining descriptor authority stay
// identical. Heap locator-only assertions deliberately remain stricter.
func ReindexRelationReplacement(t testing.TB, view catalog.Read, expected *catalog.Relation) *catalog.Relation {
	t.Helper()
	current := view.MustRelation(expected.Address())
	if current == expected || expected.SQLIndex == nil || current.SQLIndex == nil || current.SQLIndex == expected.SQLIndex {
		t.Fatal("REINDEX did not own fresh descriptor/definition clones", expected.Address())
	}
	definition := *expected.SQLIndex
	definition.Ready, definition.Valid, definition.Live = true, true, true
	if current.SQLIndex.Constraint != definition.Constraint || current.SQLIndex.Predicate != definition.Predicate || !reflect.DeepEqual(current.SQLIndex, &definition) {
		t.Fatal("REINDEX changed unrelated index definition authority", expected.Address())
	}
	if len(definition.Keys) != 0 && &current.SQLIndex.Keys[0] != &definition.Keys[0] ||
		len(definition.Include) != 0 && &current.SQLIndex.Include[0] != &definition.Include[0] ||
		len(definition.Exclusion) != 0 && &current.SQLIndex.Exclusion[0] != &definition.Exclusion[0] {
		t.Fatal("REINDEX replaced immutable key/include/exclusion recipe backing", expected.Address())
	}
	want := *expected
	want.ObjectRevision++
	want.SQLIndex = current.SQLIndex // The exact definition transition was checked above.
	return RelationLocatorReplacement(t, view, &want)
}

// ExpressionBinder is a scalar binder over a postgres session, with real
// catalog and session authorization like public SQL. The session ends at
// test cleanup.
func ExpressionBinder(t testing.TB, ctx context.Context, e *core.Engine, input sqltype.InputEnvironment, expected *core.Description) *analyze.Binder {
	t.Helper()
	op, done := RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	return core.NewExpressionBinder(ctx, e, input, op, expected)
}
