package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestThesaurusPreparedOwnership(t *testing.T) {
	t.Run("ProtocolGraphs", thesaurusProtocolOwnership)
	e, s := thesaurusCoreSetup(t)
	const query = "SELECT ts_lexize('public.thes_unit'::regdictionary,'one'::text) AS lexemes"
	ast, d := sequencePreparedDescription(t, e, s, query)
	if d.Folds == nil || len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
		t.Fatal("Describe evaluated thesaurus")
	}
	preparedFoldExecute(t, e, s, ast, &d)
	entry := preparedFoldEntry(t, d, 1009, "{1}")
	before := snapshotFoldDescription(d)
	analysis := d.Analysis
	if entry.Value.Type() != entry.Result.Type || entry.Value.ArrayElement(0).Type().ID() != sqltype.Text {
		t.Fatal("typed immutable thesaurus result")
	}
	source := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "thes_unit_sub"})
	dictionaryFoldDependency(t, d, source)
	preparedFoldExecute(t, e, s, ast, &d)
	if d.Analysis != analysis || d.Folds.Current().Generation != before.Accepted.Generation || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("unchanged invocation lost accepted owner/backing")
	}
	vectorAST, vectorDescription := sequencePreparedDescription(t, e, s, "SELECT to_tsvector('public.thes_unit_cfg','one two three') AS normalized")
	preparedFoldExecute(t, e, s, vectorAST, &vectorDescription)
	preparedFoldEntry(t, vectorDescription, 3614, "'123':1")
	vectorBefore := snapshotFoldDescription(vectorDescription)
	sourceBefore := dictionaryFoldDependency(t, d, source)
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.thes_unit_sub (Accept=false)")
	currentSource := e.CatalogRead().Get(source)
	if currentSource.Revision() == sourceBefore.Revision {
		t.Fatal("normalizer effective revision did not advance")
	}
	preparedFoldExecute(t, e, s, ast, &d)
	preparedFoldExecute(t, e, s, vectorAST, &vectorDescription)
	refreshed := dictionaryFoldDependency(t, d, source)
	if d.Analysis == analysis || refreshed.Ref != sourceBefore.Ref || refreshed.Revision != currentSource.Revision() {
		t.Fatal("current analysis did not observe normalizer revision", refreshed, sourceBefore)
	}
	if d.Folds.Current().Generation != before.Accepted.Generation || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || !samePublishedFold(entry, preparedFoldEntry(t, d, 1009, "{1}")) {
		t.Fatal("normalizer edit replaced accepted scalar fold")
	}
	if vectorDescription.Folds.Current().Generation != vectorBefore.Accepted.Generation || &vectorDescription.Folds.Current().Entries[0] != &vectorBefore.Accepted.Entries[0] {
		t.Fatal("normalizer edit replaced accepted vector backing")
	}
	preparedFoldEntry(t, vectorDescription, 3614, "'123':1")
	for _, freshQuery := range []string{query, "SELECT to_tsvector('public.thes_unit_cfg','one two three') AS normalized"} {
		freshAST, fresh := sequencePreparedDescription(t, e, s, freshQuery)
		_, failure := sequencePreparedExecute(e, s, freshAST, &fresh)
		var diagnostic *sqlstate.Error
		if !errors.As(failure, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != `thesaurus sample word "one" isn't recognized by subdictionary (rule 1)` {
			t.Fatal("fresh invocation reused accepted fold", failure)
		}
		if fresh.Folds.Current().Generation != 0 || len(fresh.Folds.Current().Entries) != 0 {
			t.Fatal("failed initialization published a fold prefix")
		}
	}
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.thes_unit_sub (Accept=true)")
	preparedFoldExecute(t, e, s, ast, &d)
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.thes_unit (DictFile=stuntgres_thes_ties)")
	preparedFoldExecute(t, e, s, ast, &d)
	preparedFoldExecute(t, e, s, vectorAST, &vectorDescription)
	if d.Folds.Current().Generation != before.Accepted.Generation || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || vectorDescription.Folds.Current().Generation != vectorBefore.Accepted.Generation || &vectorDescription.Folds.Current().Entries[0] != &vectorBefore.Accepted.Entries[0] {
		t.Fatal("own options edit replaced accepted fold ownership")
	}
	preparedFoldEntry(t, d, 1009, "{1}")
	preparedFoldEntry(t, vectorDescription, 3614, "'123':1")
	freshScalarAST, freshScalar := sequencePreparedDescription(t, e, s, query)
	preparedFoldExecute(t, e, s, freshScalarAST, &freshScalar)
	preparedFoldEntry(t, freshScalar, 1009, "NULL")
	freshVectorAST, freshVector := sequencePreparedDescription(t, e, s, "SELECT to_tsvector('public.thes_unit_cfg','one two three') AS normalized")
	preparedFoldExecute(t, e, s, freshVectorAST, &freshVector)
	preparedFoldEntry(t, freshVector, 3614, "'one':1 'three':3 'two':2")
	preparedFoldEntry(t, before, 1009, "{1}")
	preparedFoldEntry(t, vectorBefore, 3614, "'123':1")
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
	scope.Finish(err)
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed {
		t.Fatal("canceled thesaurus fold accepted", err)
	}
	if local.Folds.Current().Generation != before.Accepted.Generation || &local.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("rejected private fold replaced accepted immutable backing")
	}
	preparedFoldEntry(t, before, 1009, "{1}")
}
