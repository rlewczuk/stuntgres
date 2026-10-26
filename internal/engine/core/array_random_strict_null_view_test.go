package core_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
)

// Called by the selected StoredDefaultClosure definition. This reaches actual
// stored source preparation, not a fresh scalar binding in place of the view.
func arrayStrictNullStoredView(t *testing.T) {
	e, op, setupScope := arrayStrictNullFixture(t)
	if err := setupScope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"CREATE VIEW array_strict_literal_view AS SELECT array_sample(NULL::integer[],nextval('strict_null_steps')::integer) AS v",
		"CREATE VIEW array_strict_runtime_view AS SELECT array_sample(ARRAY[7],v+nextval('strict_null_steps')::integer) AS v FROM strict_null_rows",
		"CREATE VIEW array_strict_nonnull_view AS SELECT array_sample(ARRAY[7],nextval('strict_null_steps')::integer) AS v",
		"CREATE VIEW array_strict_unused_window AS SELECT 1 AS v WINDOW unused AS (ORDER BY nextval('strict_null_steps'))",
		"GRANT SELECT ON strict_null_rows TO array_strict_owner",
		"GRANT USAGE,SELECT ON SEQUENCE strict_null_steps TO array_strict_owner",
	} {
		enginetest.RoleExecute(t, e, op, sql)
	}
	literalRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "array_strict_literal_view")
	beforeOwner := stored.ViewOf(e.CatalogRead().MustRelation(literalRef.Address))
	if beforeOwner == nil {
		t.Fatal("missing real stored literal view definition")
	}
	originalProject, ok := beforeOwner.Query().Root.(*plan.ProjectNode)
	if !ok || len(originalProject.Expressions) != 1 {
		t.Fatal("unexpected real stored projection", beforeOwner.Query().Root)
	}
	original := originalProject.Expressions[0]
	call, ok := original.(functions.ArrayRandom)
	if !ok || call.Validate() != nil || len(call.Args) != 2 {
		t.Fatalf("stored literal view did not retain real ArrayRandom: %#v", original)
	}
	graph := arrayStrictNullSnapshot(t, original)
	manifest := slices.Clone(beforeOwner.Dependencies())
	schema := slices.Clone(originalProject.QuerySchema)
	if len(originalProject.AnalysisExpressions) != 1 {
		t.Fatal("stored literal fixture did not capture its actual ancillary carrier", originalProject.AnalysisExpressions)
	}
	analysisGraph := make([]expr.Expr, len(originalProject.AnalysisExpressions))
	for i, expression := range originalProject.AnalysisExpressions {
		if _, ok := expression.(functions.ArrayRandom); !ok {
			t.Fatal("unexpected stored ancillary original", expression)
		}
		analysisGraph[i] = arrayStrictNullSnapshot(t, expression)
	}
	assertPrivateAnalysis := func(project *plan.ProjectNode) {
		t.Helper()
		if len(project.AnalysisExpressions) != len(analysisGraph) {
			t.Fatal("private preflight removed ancillary graph instead of planning it")
		}
		for i, expression := range project.AnalysisExpressions {
			arrayStrictNullResult(t, analysisGraph[i], expression)
		}
	}
	outer := randomPlanningUnitDependency(t, e, manifest, "array_sample")
	child := randomPlanningUnitDependency(t, e, manifest, "nextval")
	selected, err := exec.ExpressionRoutineDescriptor(original)
	if err != nil || selected == nil || selected.Address() != outer.Ref.Address || selected.Revision() != outer.Revision {
		t.Fatal("stored literal view lost selected original declaration", selected, err)
	}
	declaration := *selected
	for _, sql := range []string{
		"ALTER VIEW array_strict_literal_view OWNER TO array_strict_owner",
		"ALTER VIEW array_strict_runtime_view OWNER TO array_strict_owner",
		"ALTER VIEW array_strict_nonnull_view OWNER TO array_strict_owner",
		"ALTER VIEW array_strict_unused_window OWNER TO array_strict_owner",
		"GRANT SELECT ON array_strict_literal_view,array_strict_runtime_view,array_strict_nonnull_view,array_strict_unused_window TO array_strict_reader",
	} {
		enginetest.RoleExecute(t, e, op, sql)
	}
	if err := enginetest.SetObjectACL(e, context.Background(), child.Ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	reader, done := enginetest.RoleOperation(t, e, "array_strict_reader")
	defer done()
	readerScope, err := e.StartStatement(reader)
	if err != nil {
		t.Fatal(err)
	}
	defer readerScope.Finish(nil)
	arrayStrictNullAdmitScope(t, readerScope)
	auth := eMustJSONBRoutineAuthority(t, e, reader)
	consumer := auth.Catalog.MustRelation(literalRef.Address)
	definition := stored.ViewOf(consumer)
	if definition == nil || !reflect.DeepEqual(definition.Query().Root.(*plan.ProjectNode).Expressions[0], graph) || !reflect.DeepEqual(definition.Dependencies(), manifest) {
		t.Fatal("ALTER OWNER changed immutable stored literal graph/manifest")
	}
	evaluation := exec.Context{
		EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(readerScope), Catalog: auth.Catalog, Input: readerScope.Input, Output: readerScope.Output},
		EvalFrame: exec.EvalFrame{Scope: 1, Frame: originalProject.Frame},
		EvalAdmission: exec.EvalAdmission{RoutineAuthority: auth, RoutineRecipe: true, RoutineChecks: definition.Dependencies(), RoutineConsumer: consumer},
	}
	known, err := exec.ExecutionKnownNullArgument(&evaluation, call.Args[0])
	if err != nil || !known {
		t.Fatalf("stored/owner-COW NULL supplier is not a genuine literal proof: child=%#v info=%#v err=%v", call.Args[0], expr.InfoOf(call.Args[0]), err)
	}
	for _, uses := range [][]int{nil, {0}} {
		local := evaluation
		prepared, reads, err := view.PrepareStoredExecutionRoot(&local, definition, uses)
		if err != nil {
			t.Fatal("stored view projection preflight demanded eliminated child", err)
		}
		project, ok := prepared.(*plan.ProjectNode)
		if !ok || len(project.Expressions) != 1 {
			t.Fatal("stored preparation changed output layout", prepared)
		}
		arrayStrictNullResult(t, original, project.Expressions[0])
		assertPrivateAnalysis(project)
		if !reflect.DeepEqual(project.QuerySchema, schema) {
			t.Fatal("stored NULL erasure changed frozen output schema")
		}
		if err := view.ValidateMarkedQueryJoin(&local, prepared, auth.Catalog, nil, plan.RowMark{}, false, nil, reads); err != nil {
			t.Fatal("stored preflight result revived nested nextval demand", err)
		}
	}
	// Exercise the complete production source lane too: it constructs its own
	// context/authority and captures sources after metadata preflight/admission.
	principal := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("array_strict_reader")
	e.Mu.Lock()
	prepared, source, err := e.PrepareStoredQueryLocked(readerScope, literalRef, definition, principal, plan.RowMark{}, nil, []int{0})
	if source != nil {
		source.Close()
	}
	if err != nil {
		t.Fatal("real stored source lane revived erased nextval", err)
	}
	project, ok := prepared.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != 1 {
		t.Fatal("real stored source returned wrong projection", prepared)
	}
	arrayStrictNullResult(t, original, project.Expressions[0])
	assertPrivateAnalysis(project)
	arrayStrictNullUncalled(t, e)

	// Surviving row/nonNULL calls and an unused named window still undergo
	// their established admission. Ancillary preflight is not a blanket skip.
	for _, name := range []string{"array_strict_runtime_view", "array_strict_nonnull_view", "array_strict_unused_window"} {
		controlRef := enginetest.CatalogRef(t, e, e.CatalogRead(), name)
		controlConsumer := e.CatalogRead().MustRelation(controlRef.Address)
		control := stored.ViewOf(controlConsumer)
		controlProject := control.Query().Root.(*plan.ProjectNode)
		controlOriginal := controlProject.Expressions[0]
		if name != "array_strict_unused_window" {
			if _, ok := controlOriginal.(functions.ArrayRandom); !ok || controlOriginal.Properties().RowDependent != (name == "array_strict_runtime_view") {
				t.Fatal("stored control lost actual provider/dependence", name, controlOriginal)
			}
		}
		controlGraph := arrayStrictNullSnapshot(t, controlOriginal)
		controlAnalysis := make([]expr.Expr, len(controlProject.AnalysisExpressions))
		for i, expression := range controlProject.AnalysisExpressions {
			controlAnalysis[i] = arrayStrictNullSnapshot(t, expression)
		}
		controlManifest := slices.Clone(control.Dependencies())
		local := evaluation
		local.RoutineConsumer, local.RoutineChecks = controlConsumer, control.Dependencies()
		surviving, _, err := view.PrepareStoredExecutionRoot(&local, control, []int{0})
		if err != nil {
			t.Fatal("nonimmutable stored control was evaluated during planning", name, err)
		}
		stillDemanded := false
		for _, expression := range surviving.(*plan.ProjectNode).AnalysisExpressions {
			plan.WalkExpression(expression, func(childExpr expr.Expr) {
				descriptor, err := exec.ExpressionRoutineDescriptor(childExpr)
				if err != nil {
					t.Fatal("surviving ancillary carrier", err)
				}
				if descriptor != nil && descriptor.Address() == child.Ref.Address {
					stillDemanded = true
				}
			})
		}
		if !stillDemanded {
			t.Fatal("ancillary preflight erased a surviving nextval control", name)
		}
		e.Mu.Lock()
		_, source, err = e.PrepareStoredQueryLocked(readerScope, controlRef, control, principal, plan.RowMark{}, nil, []int{0})
		if source != nil {
			source.Close()
		}
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function nextval"})
		if !reflect.DeepEqual(controlOriginal, controlGraph) || !reflect.DeepEqual(controlProject.AnalysisExpressions, controlAnalysis) || !reflect.DeepEqual(control.Dependencies(), controlManifest) {
			t.Fatal("stored control preflight/admission mutated original", name)
		}
	}
	if !reflect.DeepEqual(original, graph) || !reflect.DeepEqual(originalProject.AnalysisExpressions, analysisGraph) || !reflect.DeepEqual(definition.Dependencies(), manifest) || !reflect.DeepEqual(*selected, declaration) {
		t.Fatal("stored source preparation mutated original graph/declarations/manifests")
	}
	arrayStrictNullUncalled(t, e)
}
