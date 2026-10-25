package engine

import (
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Host catalog fixtures resolve as the real bootstrap principal, not as a
// zero/default-allow authorization view. Caller holds e.mu.
func catalogAuthorization(e *core.Engine, view catalog.Read) core.AuthorizationView {
	return e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: view, RoleRecord: e.RoleStore.Published, Catalog: view, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, view), State: authorization.RoleState{CurrentUser: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}})
}

func roleTableRef(t *testing.T, e *core.Engine, name string) ids.ObjectRef {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	ref, err := e.ResolveReference(catalogAuthorization(e, e.Catalog.Current()), statement(t, "SELECT * FROM "+name).(*parser.Select).From.(*parser.TableReference).Name)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
