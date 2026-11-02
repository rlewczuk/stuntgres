package core_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/explain"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func explainRangeNamedOwner(t *testing.T) {
	t.Helper()
	e, scope, read, relation, ref := transitionRuntimeFixture(t, 1)
	transitionRuntimeQueue(t, scope, ref, transitionRuntimeRow(t, read, 1, 5))
	restore := core.ScopeInstallTransitionTest(scope, ref, stored.TriggerInsert, "", "x")
	defer restore()
	leave := scope.ActivateSQLRoutine(nil, read)
	defer func() {
		if err := leave(nil); err != nil {
			t.Error(err)
		}
	}()
	statement := enginetest.Statement(t, "SELECT transition.key FROM x transition(key,v)")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	binding, err := core.Describe(scope.Op.Ctx, e.AnalysisHost(scope.Op), statement, auth, ids.ObjectRef{}, scope.Input, nil, nil)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	var actual *plan.NamedRelationScanNode
	plan.VisitExecutableQuery(binding.Query.Root, func(node plan.Node) {
		if named, ok := node.(*plan.NamedRelationScanNode); ok {
			actual = named
		}
	}, func(expr.Expr) {})
	if actual == nil || actual.Name != "x" || actual.RangeName != "transition" || &actual.QuerySchema[0] != &relation.Columns[0] || !reflect.DeepEqual(actual.ColumnAliases, []string{"key", "v"}) {
		e.Mu.Unlock()
		t.Fatal("missing genuine invocation-bound range owner", actual)
	}
	backing := &actual.ColumnAliases[0]
	options := explain.Options{RangeTable: true, Format: "json"}
	renderer, err := explain.NewRenderer(core.HostOf(scope).(explain.Host), auth, options)
	if err == nil {
		_, err = renderer.Command(statement, explain.Bound{Binding: binding})
	}
	typ, _ := auth.Catalog.LookupType(sqltype.JSON)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := explain.Output(scope.Op.Ctx, []catalog.Column{{Name: "QUERY PLAN", Usage: typ.Usage()}}, options, []*explain.Renderer{renderer}, nil, explain.Summaries{})
	if err != nil {
		t.Fatal(err)
	}
	text, err := rows[0][0].Type().Output(scope.Op.Ctx, rows[0][0])
	if err != nil {
		t.Fatal(err)
	}
	var document []map[string]any
	if err := json.Unmarshal([]byte(text), &document); err != nil {
		t.Fatal(err)
	}
	entries := explainRangeEntries(t, document)
	if len(entries) != 1 || entries[0]["Kind"] != "named relation" || entries[0]["Range Name"] != "transition" || !reflect.DeepEqual(entries[0]["Columns"], []any{"key", "v"}) {
		t.Fatal("invocation range lost accepted schema/alias", entries)
	}
	for _, key := range []string{"Relation OID", "Occurrence", "Required Permissions"} {
		if _, exists := entries[0][key]; exists {
			t.Fatal("invocation range invented catalog/source authority", entries)
		}
	}
	if &actual.ColumnAliases[0] != backing || &actual.QuerySchema[0] != &relation.Columns[0] {
		t.Fatal("presentation replaced actual invocation owner backing")
	}
}
