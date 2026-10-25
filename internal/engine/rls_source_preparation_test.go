package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"testing"
)

func rlsPreparationFixture(t *testing.T) (*Engine, func(), *StatementScope) {
	t.Helper()
	e, op, done, _ := policyUnitFixture(t)
	for _, sql := range []string{
		"CREATE ROLE preparation_reader",
		"GRANT USAGE ON SCHEMA public TO preparation_reader",
		"CREATE TABLE leaf(id INT)", "CREATE TABLE unrelated(id INT)",
		"CREATE VIEW helper_view AS SELECT id FROM leaf",
		"INSERT INTO t VALUES(1,TRUE)", "INSERT INTO helper VALUES(1)", "INSERT INTO leaf VALUES(1)",
		"GRANT SELECT,UPDATE ON t TO preparation_reader",
		"GRANT SELECT ON helper,helper_view TO preparation_reader",
		"ALTER TABLE t ENABLE ROW LEVEL SECURITY", "ALTER TABLE helper ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY p ON t USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id))",
		"CREATE POLICY p ON helper USING(EXISTS(SELECT 1 FROM helper_view WHERE helper_view.id=helper.id))",
		"SET ROLE preparation_reader",
	} {
		roleExecute(t, e, op, sql)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		done()
		t.Fatal(err)
	}
	return e, done, scope
}

// SQL supplies catalog metadata. Assertions concern exact preparation ownership,
// the one actual MVCC root union, and consumption, not SQL row answer goldens.
func TestRLSPolicySourcePreparation(t *testing.T) {
	e, done, scope := rlsPreparationFixture(t)
	defer done()
	defer scope.Finish(nil)
	if _, err := scope.Describe(statement(t, "SELECT id FROM t")); err != nil {
		t.Fatal(err)
	}
	refs := []ids.ObjectRef{roleTableRef(t, e, "t"), roleTableRef(t, e, "helper"), roleTableRef(t, e, "leaf")}
	unrelated := roleTableRef(t, e, "unrelated")
	e.Mu.Lock()
	scope.rlsAdmissionDepth++
	prepared, err := e.prepareQuerySourcesLocked(scope, scope.QuerySources, false, authorization.PrincipalRef{}, plan.RowMark{})
	err = e.endRowSecurityAdmissionLocked(scope, err)
	earlySnapshot := scope.View != nil
	earlyInputs := false
	for _, p := range scope.rlsPrograms {
		for _, pending := range p.sources {
			earlyInputs = earlyInputs || pending.clause.State.Source != nil
		}
	}
	programs := len(scope.rlsPrograms)
	if err == nil {
		err = e.materializePolicySourcesLocked(scope)
	}
	var source *exec.Source
	if err == nil {
		source, err = e.capturePreparedQuerySourcesLocked(scope, prepared, scope.QueryLockContext)
	}
	first := scope.View
	secondErr := error(nil)
	if err == nil {
		_, secondErr = e.capturePreparedQuerySourcesLocked(scope, prepared, scope.QueryLockContext)
	}
	e.Mu.Unlock()
	if source != nil {
		defer source.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if earlySnapshot || earlyInputs {
		t.Fatal("physical data captured before complete policy/view closure")
	}
	if first == nil || scope.View != first {
		t.Fatal("missing or replaced shared frontier")
	}
	data := first.Load()
	if len(data.Committed) != len(refs) {
		t.Fatal("snapshot did not capture exact required union", len(data.Committed))
	}
	for _, ref := range refs {
		if _, ok := data.Committed[ref.Address]; !ok {
			t.Fatal("missing prepared physical root", ref)
		}
	}
	if _, ok := data.Committed[unrelated.Address]; ok || data.DynamicRoots != nil {
		t.Fatal("blanket dynamic/all-catalog snapshot introduced")
	}
	if len(scope.rlsPrograms) != programs || !prepared.consumed {
		t.Fatal("materialization reselected or failed to consume occurrence programs")
	}
	if secondErr != ids.ErrCatalogReplaced {
		t.Fatal("prepared occurrence reused", secondErr)
	}
	for _, p := range scope.rlsPrograms {
		if len(p.sources) != 0 {
			t.Fatal("realized record retained a second pending-source owner")
		}
	}
}

func TestRLSPolicyMutationSourcePreparation(t *testing.T) {
	e, done, scope := rlsPreparationFixture(t)
	defer done()
	finished := false
	defer func() {
		if !finished {
			_ = scope.Finish(context.Canceled)
		}
	}()
	result, err := scope.Execute(statement(t, "UPDATE t SET allowed=TRUE"), 0, nil)
	if result.Rows != nil {
		defer result.Rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	refs := []ids.ObjectRef{roleTableRef(t, e, "t"), roleTableRef(t, e, "helper"), roleTableRef(t, e, "leaf")}
	if scope.View == nil {
		t.Fatal("no-source mutation omitted its prepared snapshot")
	}
	data := scope.View.Load()
	if len(data.Committed) != len(refs) {
		t.Fatal("mutation target/helper union incomplete", len(data.Committed))
	}
	for _, ref := range refs {
		if _, ok := data.Committed[ref.Address]; !ok {
			t.Fatal("mutation source became first narrow snapshot", ref)
		}
	}
	if scope.rlsAdmissionDepth != 0 || scope.rlsCommand != nil {
		t.Fatal("preparation activation leaked")
	}
	err = scope.Finish(nil)
	finished = true
	if err != nil {
		t.Fatal(err)
	}
}
