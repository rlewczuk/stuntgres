package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"testing"
)

// withPublicSchemaUnitOwner provisions only a positive CREATE or owner-successor
// precondition. It leaves the tested operation's identity and object ACLs alone.
func withPublicSchemaUnitOwner(t *testing.T, e *Engine, owner string, body func()) {
	t.Helper()
	view := e.CatalogRead()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	if len(addresses) != 1 {
		t.Fatal("public schema fixture requires one public namespace")
	}
	descriptor, ok := view.Descriptor(addresses[0])
	if !ok {
		t.Fatal("public schema fixture requires its namespace descriptor")
	}
	namespace, ok := descriptor.(*catalog.Namespace)
	if !ok {
		t.Fatal("public schema fixture has a non-namespace descriptor")
	}
	roles := e.Roles()
	prior, err := roles.Descriptor(namespace.Owner)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := roles.Descriptor(e.roles.Domain.Principal(10))
	if err != nil {
		t.Fatal(err)
	}
	login, err := e.ResolveLogin(bootstrap.Name)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: login})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sess.RequestEnd(session.CleanClose)
		if err := sess.WaitEnd(context.Background()); err != nil {
			t.Error("closing public schema fixture session:", err)
		}
	}()
	admin, err := sess.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Done()
	quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
	// Restore while the borrowed operation and the prior owner are still live,
	// including when the body terminates through testing.T.Fatal.
	defer func() {
		restore, err := parser.Parse("ALTER SCHEMA public OWNER TO " + quote(prior.Name))
		if err != nil {
			t.Error("parsing public schema owner restoration:", err)
			return
		}
		if len(restore) != 1 {
			t.Errorf("expected single public schema owner restoration statement, got %d", len(restore))
			return
		}
		if _, err := e.Execute(admin, restore[0], 0, nil); err != nil {
			t.Error("restoring public schema owner:", err)
		}
	}()
	roleExecute(t, e, admin, "ALTER SCHEMA public OWNER TO "+quote(owner))
	body()
}
