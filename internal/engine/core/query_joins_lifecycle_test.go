package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/core"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

func TestQueryJoinsStatementFrontierAndNoWidening(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE a(k INT)", "CREATE TABLE b(k INT)", "CREATE TABLE unrelated(k INT)", "INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)"} {
		enginetest.SessionSQL(t, a, q)
	}
	op := enginetest.TransactionOperation(t, a)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	ast := enginetest.Statement(t, "SELECT k FROM a JOIN b USING(k)")
	if _, err = scope.Describe(ast); err != nil {
		t.Fatal(err)
	}
	unrelated := enginetest.CatalogRef(t, e, e.CatalogRead(), "unrelated")
	var widenErr error
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var binding analyze.Binding
	if err == nil {
		binding, err = core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	var source *exec.Source
	if err == nil {
		source, err = e.CaptureQuerySourcesLocked(scope, binding.QuerySources, false)
	}
	frontier := e.Visibility.CommitSeq()
	if err == nil {
		_, widenErr = core.ScopeAcquireDataViewLocked(scope, e, unrelated)
	}
	e.Mu.Unlock()
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if widenErr == nil {
		scope.Finish(errors.New("view widened"))
		t.Fatal("retained statement view widened")
	}
	defer source.Close()
	data := source.View.Data()
	if source.Sources[1].View.Data() != data || data.Frontier != frontier || len(data.Committed) != 2 || data.GenerationAt(binding.QuerySources[0].Ref.Address) != binding.QuerySources[0].Storage || data.GenerationAt(binding.QuerySources[1].Ref.Address) != binding.QuerySources[1].Storage {
		t.Fatal(data)
	}
	peer := enginetest.TransactionBegin(t, b)
	enginetest.SessionSQL(t, b, "INSERT INTO a VALUES(2)")
	enginetest.SessionSQL(t, b, "INSERT INTO b VALUES(2)")
	enginetest.TransactionEnd(t, b, peer, true)
	if err = exec.SelectQuerySources(op.Ctx, source); err != nil {
		t.Fatal(err)
	}
	rows, n, err := exec.Finalize(op.Ctx, binding.Query.Root, source, nil)
	if err != nil || n != 1 {
		t.Fatal(rows, n, err)
	}
	values := make([]sqltype.Value, 1)
	present, err := rows.Next(context.Background(), values)
	value, _ := values[0].Integer()
	if !present || err != nil || value != 1 {
		t.Fatal(present, err, value)
	}
	rows.Close()
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	enginetest.TransactionRows(t, b, "SELECT k FROM a JOIN b USING(k) ORDER BY k", 1, 2)
	enginetest.LockEmpty(t, e)
}
