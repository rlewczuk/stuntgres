package types_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestThesaurusPreparedOwnership(t *testing.T) {
	t.Run("ProtocolGraphs", thesaurusProtocolOwnership)
	e, s := thesaurusCoreSetup(t)
	const query = "SELECT ts_lexize('public.thes_unit'::regdictionary,'one'::text) AS lexemes"
	ast, d := enginetest.PreparedDescription(t, e, s, query)
	if d.Folds == nil || len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
		t.Fatal("Describe evaluated thesaurus")
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	entry := enginetest.PreparedFoldEntry(t, d, 1009, "{1}")
	before := enginetest.SnapshotFoldDescription(d)
	analysis := d.Analysis
	if entry.Value.Type() != entry.Result.Type || entry.Value.ArrayElement(0).Type().ID() != sqltype.Text {
		t.Fatal("typed immutable thesaurus result")
	}
	source := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "thes_unit_sub"})
	enginetest.DictionaryFoldDependency(t, d, source)
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	if d.Analysis != analysis || d.Folds.Current().Generation != before.Accepted.Generation || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("unchanged invocation lost accepted owner/backing")
	}
	vectorAST, vectorDescription := enginetest.PreparedDescription(t, e, s, "SELECT to_tsvector('public.thes_unit_cfg','one two three') AS normalized")
	enginetest.PreparedFoldExecute(t, e, s, vectorAST, &vectorDescription)
	enginetest.PreparedFoldEntry(t, vectorDescription, 3614, "'123':1")
	vectorBefore := enginetest.SnapshotFoldDescription(vectorDescription)
	sourceBefore := enginetest.DictionaryFoldDependency(t, d, source)
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.thes_unit_sub (Accept=false)")
	currentSource := e.CatalogRead().Get(source)
	if currentSource.Revision() == sourceBefore.Revision {
		t.Fatal("normalizer effective revision did not advance")
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	enginetest.PreparedFoldExecute(t, e, s, vectorAST, &vectorDescription)
	refreshed := enginetest.DictionaryFoldDependency(t, d, source)
	if d.Analysis == analysis || refreshed.Ref != sourceBefore.Ref || refreshed.Revision != currentSource.Revision() {
		t.Fatal("current analysis did not observe normalizer revision", refreshed, sourceBefore)
	}
	if d.Folds.Current().Generation != before.Accepted.Generation || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, d, 1009, "{1}")) {
		t.Fatal("normalizer edit replaced accepted scalar fold")
	}
	if vectorDescription.Folds.Current().Generation != vectorBefore.Accepted.Generation || &vectorDescription.Folds.Current().Entries[0] != &vectorBefore.Accepted.Entries[0] {
		t.Fatal("normalizer edit replaced accepted vector backing")
	}
	enginetest.PreparedFoldEntry(t, vectorDescription, 3614, "'123':1")
	for _, freshQuery := range []string{query, "SELECT to_tsvector('public.thes_unit_cfg','one two three') AS normalized"} {
		freshAST, fresh := enginetest.PreparedDescription(t, e, s, freshQuery)
		_, failure := enginetest.PreparedExecute(e, s, freshAST, &fresh)
		var diagnostic *sqlstate.Error
		if !errors.As(failure, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != `thesaurus sample word "one" isn't recognized by subdictionary (rule 1)` {
			t.Fatal("fresh invocation reused accepted fold", failure)
		}
		if fresh.Folds.Current().Generation != 0 || len(fresh.Folds.Current().Entries) != 0 {
			t.Fatal("failed initialization published a fold prefix")
		}
	}
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.thes_unit_sub (Accept=true)")
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.thes_unit (DictFile=stuntgres_thes_ties)")
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	enginetest.PreparedFoldExecute(t, e, s, vectorAST, &vectorDescription)
	if d.Folds.Current().Generation != before.Accepted.Generation || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || vectorDescription.Folds.Current().Generation != vectorBefore.Accepted.Generation || &vectorDescription.Folds.Current().Entries[0] != &vectorBefore.Accepted.Entries[0] {
		t.Fatal("own options edit replaced accepted fold ownership")
	}
	enginetest.PreparedFoldEntry(t, d, 1009, "{1}")
	enginetest.PreparedFoldEntry(t, vectorDescription, 3614, "'123':1")
	freshScalarAST, freshScalar := enginetest.PreparedDescription(t, e, s, query)
	enginetest.PreparedFoldExecute(t, e, s, freshScalarAST, &freshScalar)
	enginetest.PreparedFoldEntry(t, freshScalar, 1009, "NULL")
	freshVectorAST, freshVector := enginetest.PreparedDescription(t, e, s, "SELECT to_tsvector('public.thes_unit_cfg','one two three') AS normalized")
	enginetest.PreparedFoldExecute(t, e, s, freshVectorAST, &freshVector)
	enginetest.PreparedFoldEntry(t, freshVector, 3614, "'one':1 'three':3 'two':2")
	enginetest.PreparedFoldEntry(t, before, 1009, "{1}")
	enginetest.PreparedFoldEntry(t, vectorBefore, 3614, "'123':1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	local := enginetest.CloneFoldDescription(t, d)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	cancel()
	err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	scope.Finish(err)
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed {
		t.Fatal("canceled thesaurus fold accepted", err)
	}
	if local.Folds.Current().Generation != before.Accepted.Generation || &local.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("rejected private fold replaced accepted immutable backing")
	}
	enginetest.PreparedFoldEntry(t, before, 1009, "{1}")
}

func thesaurusProtocolOwnership(t *testing.T) {
	e, s := thesaurusCoreSetup(t)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
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
			_, err := scope.BindProtocolPortal(name, "thes_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
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
	enginetest.PreparedFoldEntry(t, definition.Description, 1009, "{1}")
	view := e.CatalogRead()
	source := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "thes_unit_sub"})
	operand := enginetest.DictionaryFoldDependency(t, definition.Description, source)
	operation, done := enginetest.RoleOperation(t, e, "postgres")
	stage, err := schemacmds.StageDictionaryDetached(e, operation, enginetest.Statement(t, "ALTER TEXT SEARCH DICTIONARY public.thes_unit_sub (Accept=false)"))
	if err != nil {
		done()
		t.Fatal(err)
	}
	enginetest.PublishOperationStage(t, e, operation, stage)
	e.DiscardCatalogStage(stage)
	done()
	bind("thes_again")
	again := registry.Binding("thes_again")
	refreshed := enginetest.DictionaryFoldDependency(t, again.Entry.Description, source)
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
	enginetest.PreparedFoldEntry(t, again.Entry.Description, 1009, "{1}")
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
