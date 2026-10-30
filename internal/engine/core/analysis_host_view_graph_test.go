package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// The host checks a statement's read graph against the current catalog: tables
// and acyclic views pass, an active-path view recursion is 42P17, and a view
// that is no longer current is a catalog replacement.
func TestAnalysisHostValidatesViewReadGraph(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE base(id INT)", "CREATE VIEW v AS SELECT id FROM base", "CREATE VIEW w AS SELECT id FROM v", "CREATE VIEW loop AS SELECT id FROM base"} {
		enginetest.SessionSQL(t, s, q)
	}
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	relation := func(view catalog.Read, name string) *catalog.Relation {
		t.Helper()
		return view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: public, Name: name}))
	}
	host := e.AnalysisHost(nil)
	view := e.CatalogRead()
	if err := host.ValidateViewReadGraph(core.AuthorityOf(catalogAuthorization(e, view)), []exec.Leaf{{Relation: relation(view, "base")}, {Relation: relation(view, "w")}}); err != nil {
		t.Fatalf("acyclic read graph: %v", err)
	}
	stale := relation(view, "loop")
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE VIEW loop AS SELECT id FROM loop")
	view = e.CatalogRead()
	var diagnostic *sqlstate.Error
	if err := host.ValidateViewReadGraph(core.AuthorityOf(catalogAuthorization(e, view)), []exec.Leaf{{Relation: relation(view, "loop")}}); !errors.As(err, &diagnostic) || diagnostic.Code != "42P17" || diagnostic.Message != `infinite recursion detected in rules for relation "loop"` {
		t.Fatalf("recursive read graph = %v, want 42P17", err)
	}
	enginetest.SessionSQL(t, s, "DROP VIEW loop")
	if err := host.ValidateViewReadGraph(core.AuthorityOf(catalogAuthorization(e, e.CatalogRead())), []exec.Leaf{{Relation: stale}}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("dropped view = %v, want %v", err, ids.ErrCatalogReplaced)
	}
}
