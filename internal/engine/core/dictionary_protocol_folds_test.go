package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
)

func TestDictionaryProtocolFoldOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.dictionary_fold (TEMPLATE=pg_catalog.simple,stopwords=english)")
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "dictionary_fold"})
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
	parse := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol(name, dictionaryFoldSQL, nil)
			return err
		})
	}
	bind := func(name, definition string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, definition, &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	parse("dictionary_definition")
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("dictionary_definition")
	owner := definition.Description.Folds
	if owner == nil || len(owner.Current().Entries) != 0 || owner.Current().Generation != 0 {
		t.Fatal("Parse evaluated lexemes or omitted definition ownership")
	}
	bind("dictionary_old", "dictionary_definition")
	accepted := owner.Current()
	preparedFoldEntry(t, definition.Description, 1009, "{yes}")
	operand := dictionaryFoldDependency(t, definition.Description, address)
	analysis := definition.Description.Analysis
	old := registry.Binding("dictionary_old")
	if old == nil || old.Entry.Description.Folds != owner || old.Folds == nil || old.Resource == nil || old.Resource.Root == nil {
		t.Fatal("Bind did not accept actual shared owner and private resources")
	}
	oldRoot, oldResource := old.Resource.Root, old.Resource
	oldAccepted := *old.Folds
	if oldAccepted.Generation != accepted.Generation || len(oldAccepted.Entries) != len(accepted.Entries) || &oldAccepted.Entries[0] != &accepted.Entries[0] {
		t.Fatal("old binding copied or replaced accepted immutable backing")
	}
	for _, dependency := range accepted.Dependencies {
		if dependency.Ref.Address == address {
			t.Fatal("dictionary operand entered executable lifetime manifest")
		}
	}
	// Publish the real detached ALTER from an independent component operation.
	// This changes the committed operand while leaving the protocol binding's
	// transaction/private-context lifetime unchanged.
	op, done := enginetest.RoleOperation(t, e, "postgres")
	stage, err := schemacmds.StageDictionaryDetached(e, op, enginetest.Statement(t, "ALTER TEXT SEARCH DICTIONARY public.dictionary_fold (accept=false)"))
	if err != nil {
		done()
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, op, stage)
	e.DiscardCatalogStage(stage)
	done()
	bind("dictionary_again", "dictionary_definition")
	again := registry.Binding("dictionary_again")
	e.Mu.Lock()
	current := e.PrivateCatalogLocked(s.Transactions.Catalog).Get(address)
	e.Mu.Unlock()
	refreshed := dictionaryFoldDependency(t, again.Entry.Description, address)
	if again.Entry.Description.Analysis == analysis || refreshed.Ref != operand.Ref || refreshed.Revision == operand.Revision || refreshed.Revision != current.Revision() {
		t.Fatal("repeated Bind skipped dictionary operand reanalysis", operand, refreshed)
	}
	if registry.Lookup("dictionary_definition").Description.Folds != owner || again.Entry.Description.Folds != owner || again.Folds == nil || again.Folds.Generation != accepted.Generation || &again.Folds.Entries[0] != &accepted.Entries[0] || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("operand reanalysis replaced accepted definition/binding lexeme ownership")
	}
	if again.Resource == nil || again.Resource == oldResource || again.Resource.Root == nil || again.Resource.Root == oldRoot || old.Resource != oldResource || old.Resource.Root != oldRoot || old.Retired || &old.Folds.Entries[0] != &oldAccepted.Entries[0] {
		t.Fatal("repeated Bind reused or mutated another binding's private graph/resources")
	}
	preparedFoldEntry(t, again.Entry.Description, 1009, "{yes}")
	parse("dictionary_fresh")
	bind("dictionary_new", "dictionary_fresh")
	fresh := registry.Lookup("dictionary_fresh").Description.Folds
	if fresh == owner || registry.Binding("dictionary_new").Folds == nil || &fresh.Current().Entries[0] == &accepted.Entries[0] {
		t.Fatal("fresh definition adopted retained lexeme ownership")
	}
	preparedFoldEntry(t, registry.Lookup("dictionary_fresh").Description, 1009, "NULL")
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("dictionary_definition") })
	if owner.Live() || old.Retired || again.Retired || old.Resource.Root != oldRoot || &old.Folds.Entries[0] != &accepted.Entries[0] || &again.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("definition close mutated accepted binding lifetime")
	}
	for _, name := range []string{"dictionary_old", "dictionary_again", "dictionary_new"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	if !old.Retired || !again.Retired {
		t.Fatal("portal close retained live binding ownership")
	}
	op, _, err = s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
