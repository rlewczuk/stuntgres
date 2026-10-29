package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
)

func thesaurusProtocolOwnership(t *testing.T) {
	e, s := thesaurusCoreSetup(t)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("thes_definition", "SELECT ts_lexize('public.thes_unit','one') AS lexemes", nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("thes_definition")
	owner := definition.Description.Folds
	if owner == nil || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse evaluated thesaurus")
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "thes_definition", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("thes_old")
	accepted := owner.Current()
	old := registry.Binding("thes_old")
	if old == nil || old.Folds == nil || old.Resource == nil || old.Resource.Root == nil || old.Entry.Description.Folds != owner {
		t.Fatal("missing actual binding/definition owners")
	}
	oldRoot, oldResource := old.Resource.Root, old.Resource
	oldAccepted := *old.Folds
	analysis := definition.Description.Analysis
	if oldAccepted.Generation != accepted.Generation || &oldAccepted.Entries[0] != &accepted.Entries[0] {
		t.Fatal("binding copied immutable fold backing")
	}
	preparedFoldEntry(t, definition.Description, 1009, "{1}")
	view := e.CatalogRead()
	source := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "thes_unit_sub"})
	operand := dictionaryFoldDependency(t, definition.Description, source)
	operation, done := enginetest.RoleOperation(t, e, "postgres")
	stage, err := schemacmds.StageDictionaryDetached(e, operation, enginetest.Statement(t, "ALTER TEXT SEARCH DICTIONARY public.thes_unit_sub (Accept=false)"))
	if err != nil {
		done()
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, operation, stage)
	e.DiscardCatalogStage(stage)
	done()
	bind("thes_again")
	again := registry.Binding("thes_again")
	refreshed := dictionaryFoldDependency(t, again.Entry.Description, source)
	e.Mu.Lock()
	current := e.PrivateCatalogLocked(s.Transactions.Catalog).Get(source)
	e.Mu.Unlock()
	if again.Entry.Description.Analysis == analysis || refreshed.Ref != operand.Ref || refreshed.Revision == operand.Revision || refreshed.Revision != current.Revision() {
		t.Fatal("normalizer analysis input not refreshed", operand, refreshed)
	}
	if registry.Lookup("thes_definition").Description.Folds != owner || again.Entry.Description.Folds != owner || again.Folds == nil || again.Folds.Generation != accepted.Generation || &again.Folds.Entries[0] != &accepted.Entries[0] || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("reanalysis replaced accepted definition/binding folds")
	}
	if again.Resource == nil || again.Resource == oldResource || again.Resource.Root == nil || again.Resource.Root == oldRoot || old.Resource != oldResource || old.Resource.Root != oldRoot || old.Retired || &old.Folds.Entries[0] != &oldAccepted.Entries[0] {
		t.Fatal("reanalysis mutated original graph/resource ownership")
	}
	preparedFoldEntry(t, again.Entry.Description, 1009, "{1}")
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("thes_definition") })
	if owner.Live() || old.Retired || again.Retired || old.Resource.Root != oldRoot || &old.Folds.Entries[0] != &accepted.Entries[0] || &again.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("definition close mutated accepted bindings")
	}
	for _, name := range []string{"thes_old", "thes_again"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	if !old.Retired || !again.Retired {
		t.Fatal("closed portals retained graph ownership")
	}
	operation, _, err = s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(operation, cycle)
	operation.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
