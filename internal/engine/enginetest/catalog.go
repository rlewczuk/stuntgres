package enginetest

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CatalogAuthorization is the authorization view of the bootstrap principal
// over view, as host catalog fixtures resolve. The caller holds e.Mu.
func CatalogAuthorization(e *engine.Engine, view catalog.Read) engine.AuthorizationView {
	return e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: view, RoleRecord: e.RoleStore.Published, Catalog: view, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, view), State: authorization.RoleState{CurrentUser: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}})
}

// CatalogRef resolves the relation public.name in view as the bootstrap
// principal.
func CatalogRef(t testing.TB, e *engine.Engine, view catalog.Read, name string) ids.ObjectRef {
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
func RoleTableRef(t testing.TB, e *engine.Engine, name string) ids.ObjectRef {
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
func PublishCatalog(e *engine.Engine, p *engine.PreparedCatalogPublication, token engine.PublicationToken) (engine.ChangeSet, error) {
	return e.PublishPreparedCatalog(nil, nil, p, token)
}

// ValidateObjectRef checks, under e.Mu, that view and ref belong to e.
func ValidateObjectRef(e *engine.Engine, view catalog.Read, ref ids.ObjectRef) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.Catalog.ValidateObjectRefLocked(view, ref)
}

// ExpressionBinder is a scalar binder over a postgres session, with real
// catalog and session authorization like public SQL. The session ends at
// test cleanup.
func ExpressionBinder(t testing.TB, ctx context.Context, e *engine.Engine, input sqltype.InputEnvironment, expected *engine.StatementDescription) *analyze.Binder {
	t.Helper()
	op, done := RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	return engine.NewExpressionBinder(ctx, e, input, op, expected)
}
