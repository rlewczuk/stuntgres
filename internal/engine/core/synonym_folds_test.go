package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const synonymFoldSQL = "SELECT ts_lexize('public.synonym_fold'::regdictionary,'POSTGRES'::text) AS lexemes"

func synonymRuntimeExecute(e *core.Engine, s *session.Session, ast parser.Statement, d *core.Description) (core.Result, error) {
	op, err := s.Acquire(context.Background())
	if err != nil {
		return core.Result{}, err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return core.Result{}, err
	}
	scope.Expected = d
	r, err := scope.Execute(ast, 1, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
		value, err := info.Types[0].Type.InputWithEnvironment(ctx, "POSTGRES", -1, input)
		frame[0] = value
		return err
	})
	return r, scope.Finish(err)
}

func TestSynonymPreparedOwnership(t *testing.T) {
	t.Run("OperandRevisionAndAcceptedBacking", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.synonym_fold (TEMPLATE=pg_catalog.synonym,Synonyms=synonym_sample)")
		address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "synonym_fold"})
		ast, d := sequencePreparedDescription(t, e, s, synonymFoldSQL)
		if d.Folds == nil || len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
			t.Fatal("Describe evaluated synonym")
		}
		preparedFoldExecute(t, e, s, ast, &d)
		entry := preparedFoldEntry(t, d, 1009, "{pgsql}")
		if entry.Value.Type() != entry.Result.Type || entry.Value.ArrayLen() != 1 || entry.Value.ArrayElement(0).Type().ID() != sqltype.Text {
			t.Fatal("lexeme checked array ownership")
		}
		before := snapshotFoldDescription(d)
		operand := dictionaryFoldDependency(t, d, address)
		analysis := d.Analysis
		for _, dependency := range before.Accepted.Dependencies {
			if dependency.Ref.Address == address {
				t.Fatal("operand leaked into executable fold lifetime")
			}
		}
		runtimeAST, runtime := sequencePreparedDescription(t, e, s, "SELECT ts_lexize('public.synonym_fold'::regdictionary,$1::text) AS lexemes")
		checkRuntime := func(want any) {
			r, err := synonymRuntimeExecute(e, s, runtimeAST, &runtime)
			if err != nil {
				t.Fatal(err)
			}
			got := enginetest.Tuples(r)
			r.Rows.Close()
			if len(got) != 1 || len(got[0]) != 1 || got[0][0] != want {
				t.Fatal("runtime versus retained constant", got, want)
			}
		}
		checkRuntime("{pgsql}")
		enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.synonym_fold (CaseSensitive=true)")
		current := e.CatalogRead().Get(address)
		if current.Revision() == operand.Revision {
			t.Fatal("effective dictionary revision did not advance")
		}
		preparedFoldExecute(t, e, s, ast, &d)
		after := d.Folds.Current()
		refreshed := dictionaryFoldDependency(t, d, address)
		if d.Analysis == analysis || refreshed.Ref != operand.Ref || refreshed.Revision != current.Revision() {
			t.Fatal("operand analysis not refreshed", refreshed, operand)
		}
		if after.Generation != before.Accepted.Generation || len(after.Entries) != len(before.Accepted.Entries) || &after.Entries[0] != &before.Accepted.Entries[0] || !samePublishedFold(entry, preparedFoldEntry(t, d, 1009, "{pgsql}")) {
			t.Fatal("operand ALTER replaced accepted backing/provenance")
		}
		checkRuntime(nil)
		freshAST, fresh := sequencePreparedDescription(t, e, s, synonymFoldSQL)
		preparedFoldExecute(t, e, s, freshAST, &fresh)
		preparedFoldEntry(t, fresh, 1009, "NULL")
		preparedFoldEntry(t, before, 1009, "{pgsql}")
	})

	t.Run("ProtocolDefinitionAndGraphLifetime", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.synonym_fold (TEMPLATE=synonym,Synonyms=synonym_sample)")
		enginetest.SessionSQL(t, s, "BEGIN")
		cycle := s.NewProtocolCycle()
		command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("synonym_definition", synonymFoldSQL, nil)
			return err
		})
		registry := core.PayloadOf(s).PreparedSQL
		definition := registry.Lookup("synonym_definition")
		owner := definition.Description.Folds
		if owner == nil || len(owner.Current().Entries) != 0 {
			t.Fatal("Parse did not retain an unevaluated real definition")
		}
		bind := func(name string) {
			command(func(scope *core.StatementScope) error {
				_, err := scope.BindProtocolPortal(name, "synonym_definition", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
				return err
			})
		}
		bind("synonym_old")
		old := registry.Binding("synonym_old")
		accepted := owner.Current()
		if old == nil || old.Folds == nil || old.Entry.Description.Folds != owner || old.Resource == nil || old.Resource.Root == nil || &old.Folds.Entries[0] != &accepted.Entries[0] {
			t.Fatal("actual binding ownership missing")
		}
		oldRoot, oldResource, oldAccepted := old.Resource.Root, old.Resource, *old.Folds
		preparedFoldEntry(t, definition.Description, 1009, "{pgsql}")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		stage, err := schemacmds.StageDictionaryDetached(e, op, enginetest.Statement(t, "ALTER TEXT SEARCH DICTIONARY public.synonym_fold (CaseSensitive=true)"))
		if err != nil {
			done()
			t.Fatal(err)
		}
		extensionUnitPublish(t, e, op, stage)
		e.DiscardCatalogStage(stage)
		done()
		bind("synonym_again")
		again := registry.Binding("synonym_again")
		if again == nil || again.Folds == nil || again.Entry.Description.Folds != owner || again.Folds.Generation != accepted.Generation || &again.Folds.Entries[0] != &accepted.Entries[0] || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] {
			t.Fatal("reanalysis replaced accepted definition backing")
		}
		if again.Resource == nil || again.Resource == oldResource || again.Resource.Root == nil || again.Resource.Root == oldRoot || old.Resource != oldResource || old.Resource.Root != oldRoot || old.Retired || &old.Folds.Entries[0] != &oldAccepted.Entries[0] {
			t.Fatal("binding graph/resource lifetime corrupted")
		}
		if !samePublishedFold(oldAccepted.Entries[0], again.Folds.Entries[0]) {
			t.Fatal("retained fold provenance changed")
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("synonym_definition") })
		if owner.Live() || old.Retired || again.Retired || old.Resource.Root != oldRoot || &old.Folds.Entries[0] != &accepted.Entries[0] {
			t.Fatal("definition close mutated accepted binding")
		}
		for _, name := range []string{"synonym_old", "synonym_again"} {
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
		}
		if !old.Retired || !again.Retired {
			t.Fatal("portal close retained live ownership")
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
	})

	t.Run("FailedDemandPublishesNothing", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.synonym_fold (TEMPLATE=synonym,Synonyms=synonym_sample)")
		ast, d := sequencePreparedDescription(t, e, s, synonymFoldSQL+",(1/0)::bigint")
		if _, err := sequencePreparedExecute(e, s, ast, &d); err == nil || len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
			t.Fatal("failed demand published synonym prefix", err)
		}
	})

	t.Run("CanceledExactPublication", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.synonym_fold (TEMPLATE=synonym,Synonyms=synonym_sample)")
		ast, d := sequencePreparedDescription(t, e, s, synonymFoldSQL)
		preparedFoldExecute(t, e, s, ast, &d)
		before := snapshotFoldDescription(d)
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
		if _, err = scope.Describe(ast); err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		local := cloneFoldDescription(t, d)
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
		_ = scope.Finish(err)
		if !errors.Is(err, context.Canceled) || publication.Group.Sealed {
			t.Fatal("canceled synonym candidate accepted", err)
		}
		if local.Folds.Current().Generation != before.Accepted.Generation || &local.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
			t.Fatal("canceled candidate changed accepted owner")
		}
		preparedFoldEntry(t, before, 1009, "{pgsql}")
	})

	t.Run("CurrentAuthorityRejectsReuse", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY public.synonym_fold (TEMPLATE=synonym,Synonyms=synonym_sample)", "CREATE USER synonym_fold_reader", "CREATE TABLE synonym_fold_authority(v int)", "INSERT INTO synonym_fold_authority VALUES(1)", "GRANT SELECT ON synonym_fold_authority TO synonym_fold_reader", "SET SESSION AUTHORIZATION synonym_fold_reader"} {
			enginetest.SessionSQL(t, s, q)
		}
		ast, d := sequencePreparedDescription(t, e, s, synonymFoldSQL+" FROM synonym_fold_authority")
		preparedFoldExecute(t, e, s, ast, &d)
		before := snapshotFoldDescription(d)
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "REVOKE SELECT ON synonym_fold_authority FROM synonym_fold_reader", "SET SESSION AUTHORIZATION synonym_fold_reader", "DISCARD PLANS"} {
			enginetest.SessionSQL(t, s, q)
		}
		_, err := sequencePreparedExecute(e, s, ast, &d)
		extensionUnitError(t, err, "42501")
		if &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || foldRootsReusable(d.Folds) {
			t.Fatal("authority rejection accepted synonym candidate")
		}
		preparedFoldEntry(t, before, 1009, "{pgsql}")
	})
}
