package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const dictionaryFoldSQL = "SELECT ts_lexize('public.dictionary_fold'::regdictionary,'YeS'::text) AS lexemes"

func dictionaryFoldDependency(t *testing.T, d core.Description, address ids.ObjectAddress) catalog.ExpressionDependency {
	t.Helper()
	for _, dependency := range d.Analysis.Dependencies {
		if dependency.Ref.Address == address {
			if dependency.Purpose != catalog.OperandDependency {
				t.Fatal("dictionary operand acquired executable purpose", dependency)
			}
			return dependency
		}
	}
	t.Fatal("dictionary identity missing from analysis manifest")
	return catalog.ExpressionDependency{}
}

func dictionaryFoldRuntimeExecute(e *core.Engine, s *session.Session, ast parser.Statement, d *core.Description) (core.Result, error) {
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
		value, err := info.Types[0].Type.InputWithEnvironment(ctx, "YeS", -1, input)
		frame[0] = value
		return err
	})
	return r, scope.Finish(err)
}

func TestDictionaryPreparedFoldOwnership(t *testing.T) {
	t.Run("OperandRevisionAndAcceptedBacking", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.dictionary_fold (TEMPLATE=pg_catalog.simple,stopwords=english)")
		address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "dictionary_fold"})
		ast, d := sequencePreparedDescription(t, e, s, dictionaryFoldSQL)
		if d.Folds == nil || len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
			t.Fatal("Describe evaluated dictionary constant")
		}
		preparedFoldExecute(t, e, s, ast, &d)
		entry := preparedFoldEntry(t, d, 1009, "{yes}")
		if entry.Value.Type() != entry.Result.Type || entry.Value.ArrayLen() != 1 || entry.Value.ArrayElement(0).Type().ID() != sqltype.Text {
			t.Fatal("retained lexemes lost checked array ownership")
		}
		before := snapshotFoldDescription(d)
		operand := dictionaryFoldDependency(t, d, address)
		analysis := d.Analysis
		if operand.Revision != e.CatalogRead().Get(address).Revision() {
			t.Fatal("original dictionary authority stamp was not captured")
		}
		for _, dependency := range before.Accepted.Dependencies {
			if dependency.Ref.Address == address {
				t.Fatal("dictionary operand leaked into executable fold manifest")
			}
		}
		runtimeAST, runtime := sequencePreparedDescription(t, e, s, "SELECT ts_lexize('public.dictionary_fold'::regdictionary,$1::text) AS lexemes")
		r, err := dictionaryFoldRuntimeExecute(e, s, runtimeAST, &runtime)
		if err != nil {
			t.Fatal(err)
		}
		got := enginetest.Tuples(r)
		r.Rows.Close()
		if len(got) != 1 || len(got[0]) != 1 || got[0][0] != "{yes}" {
			t.Fatal("initial runtime-token result", got)
		}
		enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.dictionary_fold (accept=false)")
		current := e.CatalogRead().Get(address)
		if current.Revision() == operand.Revision {
			t.Fatal("ALTER did not advance effective dictionary revision")
		}
		preparedFoldExecute(t, e, s, ast, &d)
		after := d.Folds.Current()
		refreshed := dictionaryFoldDependency(t, d, address)
		if d.Analysis == analysis || refreshed.Ref != operand.Ref || refreshed.Revision != current.Revision() {
			t.Fatal("retention bypassed current dictionary analysis", refreshed, operand)
		}
		if after.Generation != before.Accepted.Generation || len(after.Entries) != len(before.Accepted.Entries) || &after.Entries[0] != &before.Accepted.Entries[0] || !samePublishedFold(entry, preparedFoldEntry(t, d, 1009, "{yes}")) {
			t.Fatal("operand-only ALTER replaced accepted immutable backing")
		}
		r, err = dictionaryFoldRuntimeExecute(e, s, runtimeAST, &runtime)
		if err != nil {
			t.Fatal(err)
		}
		got = enginetest.Tuples(r)
		r.Rows.Close()
		if len(got) != 1 || len(got[0]) != 1 || got[0][0] != nil {
			t.Fatal("runtime-token call reused constant state", got)
		}
		freshAST, fresh := sequencePreparedDescription(t, e, s, dictionaryFoldSQL)
		preparedFoldExecute(t, e, s, freshAST, &fresh)
		preparedFoldEntry(t, fresh, 1009, "NULL")
		preparedFoldEntry(t, before, 1009, "{yes}")
		enginetest.SessionSQL(t, s, "DISCARD PLANS")
		preparedFoldExecute(t, e, s, ast, &d)
		if d.Folds.Current().Generation == before.Accepted.Generation || &d.Folds.Current().Entries[0] == &before.Accepted.Entries[0] {
			t.Fatal("DISCARD retained retired root generation")
		}
		preparedFoldEntry(t, d, 1009, "NULL")
		preparedFoldEntry(t, before, 1009, "{yes}")
	})

	t.Run("FailedDemandPublishesNothing", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.dictionary_fold (TEMPLATE=simple,stopwords=english)")
		ast, d := sequencePreparedDescription(t, e, s, dictionaryFoldSQL+",(1/0)::bigint")
		if _, err := sequencePreparedExecute(e, s, ast, &d); err == nil || len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
			t.Fatal("later demanded failure published dictionary prefix", err)
		}
	})

	t.Run("CanceledExactPublication", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.dictionary_fold (TEMPLATE=simple,stopwords=english)")
		ast, d := sequencePreparedDescription(t, e, s, dictionaryFoldSQL)
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
			t.Fatal("canceled dictionary fold publication accepted", err)
		}
		if local.Folds.Current().Generation != before.Accepted.Generation || &local.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
			t.Fatal("rejected private dictionary group changed accepted backing")
		}
		preparedFoldEntry(t, before, 1009, "{yes}")
	})

	t.Run("CurrentAuthorityRejectsReuse", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY public.dictionary_fold (TEMPLATE=simple,stopwords=english)", "CREATE USER dictionary_fold_reader", "CREATE TABLE dictionary_fold_authority(v int)", "INSERT INTO dictionary_fold_authority VALUES(1)", "GRANT SELECT ON dictionary_fold_authority TO dictionary_fold_reader", "SET SESSION AUTHORIZATION dictionary_fold_reader"} {
			enginetest.SessionSQL(t, s, q)
		}
		ast, d := sequencePreparedDescription(t, e, s, dictionaryFoldSQL+" FROM dictionary_fold_authority")
		preparedFoldExecute(t, e, s, ast, &d)
		before := snapshotFoldDescription(d)
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "REVOKE SELECT ON dictionary_fold_authority FROM dictionary_fold_reader", "SET SESSION AUTHORIZATION dictionary_fold_reader", "DISCARD PLANS"} {
			enginetest.SessionSQL(t, s, q)
		}
		_, err := sequencePreparedExecute(e, s, ast, &d)
		extensionUnitError(t, err, "42501")
		if &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || foldRootsReusable(d.Folds) {
			t.Fatal("authority rejection accepted dictionary candidate")
		}
		preparedFoldEntry(t, before, 1009, "{yes}")
	})
}
