package stored_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
)

func TestIndexRecipeLiveHostCatalog(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE FUNCTION index_host_key(i INT) RETURNS INT LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN RETURN i+1; END $$",
		"CREATE TABLE index_host_recipe(v INT)",
		"INSERT INTO index_host_recipe VALUES(1)",
		"CREATE INDEX index_host_ix ON index_host_recipe(index_host_key(v))",
	} {
		enginetest.SessionSQL(t, s, query)
	}
	indexRef := enginetest.RoleTableRef(t, e, "index_host_ix")
	captured := e.CatalogRead()
	descriptor := captured.MustRelation(indexRef.Address)
	owner := captured.MustRelation(descriptor.SQLIndex.Parent)
	key := descriptor.SQLIndex.Keys[0].Derived
	call, ok := key.Definition.(plan.SQLRoutineExpr)
	if !ok {
		t.Fatal("missing genuinely bound SQL routine recipe")
	}
	originalBody := call.Procedure.SQL
	root := e.Storage.IndexRoots[indexRef.Address]
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION index_host_key(i INT) RETURNS INT LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN RETURN i+2; END $$")
	if e.CatalogRead().MustProcedure(call.Ref.Address).SQL == originalBody {
		t.Fatal("replacement did not create a current body")
	}

	// A real active engine statement owns the admission host. The recipe has
	// neither explicit Catalog nor RoutineAuthority, like partition attach's
	// derived-key validation path; it must resolve the host's live authority.
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("unfinished index recipe catalog witness"))
	result, err := scope.Execute(enginetest.Statement(t, "SELECT v FROM index_host_recipe"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.ActivateRuntime(op.Context()), Host: core.HostOf(scope), Catalog: catalog.Read{}}}
	var admission exec.RoutineProgram
	prepared, err := index.PrepareIndexRecipe(&evaluation, key.Definition, key.Dependencies, owner, &admission, true)
	if err != nil {
		t.Fatal("live host catalog preparation", err)
	}
	local := index.RecipeEvaluation(&evaluation, key.Dependencies, owner)
	local.RoutineProgram = &admission
	local.Row = e.Storage.Relations[owner.Address()].Versions[0].Values
	local.Scope, local.Frame = 1, 1
	value, err := prepared.Eval(&local)
	if err != nil {
		t.Fatal("live host current body evaluation", err)
	}
	if got, err := value.Integer(); err != nil || got != 3 {
		t.Fatal("recipe did not execute the current body", got, err)
	}
	if evaluation.Catalog.State() != nil || evaluation.RoutineAuthority != nil || evaluation.RoutineConsumer != nil {
		t.Fatal("recipe preparation overwrote enclosing authority")
	}
	ordering, err := index.BindIndexOrdering(e.CatalogRead(), owner, descriptor.SQLIndex)
	if err != nil {
		t.Fatal(err)
	}
	var keys index.ExecutionPreparation
	if err := keys.PrepareKeys(&evaluation, owner, descriptor.SQLIndex, &ordering, nil); err != nil {
		t.Fatal("later key admission did not resolve canonical live authority", err)
	}
	if evaluation.Catalog.State() != nil || evaluation.RoutineAuthority != nil || evaluation.RoutineConsumer != nil {
		t.Fatal("later key preparation overwrote enclosing authority")
	}
	retainedCall, ok := key.Definition.(plan.SQLRoutineExpr)
	if !ok || retainedCall.Procedure != call.Procedure || retainedCall.Procedure.SQL != originalBody || e.Storage.IndexRoots[indexRef.Address] != root {
		t.Fatal("executable refresh mutated stored recipe/root")
	}
	if got, err := root.Entries[0].Keys[0].Integer(); err != nil || got != 2 {
		t.Fatal("preparation replayed index publication", got, err)
	}

	// An explicit captured catalog remains a real fence. A live host cannot
	// silently replace it with its newer declaration to rescue stale admission.
	explicit := evaluation
	explicit.Catalog = captured
	var oldAdmission exec.RoutineProgram
	if _, err := index.PrepareIndexRecipe(&explicit, key.Definition, key.Dependencies, owner, &oldAdmission, true); err == nil || !strings.Contains(err.Error(), "stale routine declaration carrier") {
		t.Fatal("explicit captured catalog fence was replaced", err)
	}
	if explicit.Catalog.State() != captured.State() {
		t.Fatal("explicit catalog was mutated")
	}
	missing := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	var missingAdmission exec.RoutineProgram
	if _, err := index.PrepareIndexRecipe(&missing, key.Definition, key.Dependencies, owner, &missingAdmission, true); err == nil || !strings.Contains(err.Error(), "index recipe lacks catalog admission authority") {
		t.Fatal("missing host supplied recipe authority", err)
	}

	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	var endedAdmission exec.RoutineProgram
	if _, err := index.PrepareIndexRecipe(&evaluation, key.Definition, key.Dependencies, owner, &endedAdmission, true); err == nil || !strings.Contains(err.Error(), "ended routine admission authority") {
		t.Fatal("ended live host supplied recipe authority", err)
	}
}
