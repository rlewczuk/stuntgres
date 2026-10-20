package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Publish every incidental definition before retaining statement admission.
// Both engines use the same allocation order for equal-OID foreign probes.
func jsonRecordRoutineUnitFixture(t *testing.T) (*Engine, *Operation, *StatementScope) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, query := range []string{
		"CREATE TABLE strict_null_rows(v integer)",
		"CREATE SEQUENCE strict_null_steps START 1",
		"CREATE DOMAIN strict_null_checked AS integer CONSTRAINT strict_null_present CHECK(VALUE IS NOT NULL)",
		"CREATE VIEW json_authority_recipe AS SELECT * FROM json_to_record('{}'::json) AS j(a integer,b text)",
	} {
		roleExecute(t, e, op, query)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	return e, op, scope
}

// Exercise the actual selected carriers and their owning manifests, independently
// of the finite production inventory and of public SQL response scenarios.
func TestJSONRecordRoutineOriginalAuthority(t *testing.T) {
	e, op, scope := jsonRecordRoutineUnitFixture(t)
	other, otherOp, otherScope := jsonRecordRoutineUnitFixture(t)
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	for _, tc := range []struct {
		name, expression, signature string
		oid, result                 ids.OID
		strict, set, anonymous      bool
		defaults, base              int
	}{
		{"json_populate_record", "json_populate_record(NULL::strict_null_rows,'{}'::json)", "2283,114,16", 3960, 2283, false, false, false, 1, 0},
		{"json_populate_recordset", "json_populate_recordset(from_json => '[]'::json,base => NULL::strict_null_rows)", "2283,114,16", 3961, 2283, false, true, false, 1, 1},
		{"json_to_record", "json_to_record('{}'::json)", "114", 3204, 2249, true, false, true, 0, -1},
		{"json_to_recordset", "json_to_recordset('[]'::json)", "114", 3205, 2249, false, true, true, 0, -1},
		{"populate_anonymous", "json_populate_record(NULL::record,'{}'::json)", "2283,114,16", 3960, 2283, false, false, true, 1, 0},
		{"populate_set_anonymous", "json_populate_recordset(NULL::record,'[]'::json)", "2283,114,16", 3961, 2283, false, true, true, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
			call, ok := node.(functions.JSONRecord)
			if !ok {
				t.Fatal("original JSON record carrier", node)
			}
			arity := 1
			if tc.base >= 0 {
				arity = 2
			}
			if len(call.Args) != arity || call.Plan.BaseIndex() != tc.base || call.Defined || call.Plan.ResultUsage() != call.ResultUsage() || call.Plan.ReturnsSet() != tc.set || sourceReturnsSet(call) != tc.set || !sourceAdmitted(call) || binder.clause.queryHasTargetSet != tc.set {
				t.Fatal("physical arity/base/defined/source/project identity", call)
			}
			if call.Properties().Volatility != expr.Stable || call.Properties().StageDependent != tc.set || call.Properties().DomainDependent || call.Properties().RuntimeDomainConversion {
				t.Fatal("original stable/stage/domain properties", call.Properties())
			}
			jsonIndex := 0
			if tc.base >= 0 {
				jsonIndex = 1 - tc.base
				if call.ResultUsage() != call.Args[tc.base].ResultUsage() {
					t.Fatal("canonical anyelement replaced physical base result")
				}
			}
			if call.Args[jsonIndex].ResultUsage().Type.ID() != sqltype.JSON || tc.anonymous != call.ResultUsage().Type.IsDeferredRecord() {
				t.Fatal("SQL-order JSON/base and physical result usages", call)
			}
			captured, err := routine.ExpressionRoutineDescriptor(call)
			if err != nil || captured == nil || captured.Builtin == nil {
				t.Fatal("missing genuine selected declaration", captured, err)
			}
			address := ids.ObjectAddress{ids.ProcedureClassID, tc.oid, 0}
			declaration := captured.Builtin
			if captured.Address() != address || captured.Namespace() != (ids.ObjectAddress{ids.NamespaceClassID, 11, 0}) || captured.Name() != call.Plan.Name() || string(captured.Signature) != tc.signature || captured.Result != tc.result || captured.RoutineKind != catalog.FunctionRoutine || captured.Strict != tc.strict || captured.Volatility != expr.Stable || captured.Revision() == 0 || len(captured.Body) != 0 {
				t.Fatal("independent released identity", captured)
			}
			if declaration.Kind != catalog.FunctionRoutine || declaration.Result != tc.result || declaration.Strict != tc.strict || declaration.Volatility != expr.Stable || declaration.Parallel != 's' || declaration.ReturnsSet != tc.set || declaration.Variadic != 0 || int(declaration.Defaults) != tc.defaults || declaration.Providers&functions.JSONRoutineProvider == 0 {
				t.Fatal("independent released full-IN/default/strict/set metadata", declaration)
			}
			if evaluation.catalog.Get(address) != captured {
				t.Fatal("call did not borrow the original owned descriptor")
			}
			original := append([]catalog.ExpressionDependency(nil), binder.dependencies...)
			selected := 0
			for _, dependency := range original {
				if dependency.Ref.Address == address {
					selected++
					if dependency.Ref.Domain() != e.catalog.Domain || dependency.Ref.Epoch != evaluation.catalog.Epoch() || dependency.Revision != captured.Revision() {
						t.Fatal("original canonical ref/revision", dependency)
					}
				} else if dependency.Ref.Address.ClassID == ids.ProcedureClassID {
					t.Fatal("implicit JSON codec/helper/body EXECUTE edge", dependency)
				}
			}
			if selected != 1 {
				t.Fatal("selected declaration must have one original manifest entry", selected)
			}
			before := call
			before.Args = append([]expr.Expr(nil), call.Args...)
			rebuilt, err := call.Reconstruct(call.Info, call.Args)
			if err != nil || !reflect.DeepEqual(rebuilt, call) {
				t.Fatal("reconstruct changed original physical carrier", err)
			}
			if tc.anonymous {
				// The real FROM binder and occurrence-local shape completion must
				// retain the exact original selected declaration, not select again.
				raw := statement(t, "SELECT "+tc.expression).(*parser.Select).Targets[0].(parser.FunctionExpr)
				_, sourceBinder, _ := strictNullUnitBind(t, e, op, scope, tc.expression)
				sourceBinder.clause.aggregateClause = "FROM"
				sourceBinder.query.namespace = &queryNamespace{}
				e.mu.Lock()
				source, sourceErr := sourceBinder.bindFunctionSource(raw)
				e.mu.Unlock()
				if sourceErr != nil {
					t.Fatal("actual function source binding", sourceErr)
				}
				completed, err := sourceBinder.completeFunctionDefinitions(raw, source, []parser.FunctionColumnDefinition{
					{Name: parser.Identifier{Text: "a"}, Type: parser.TypeName{Name: "strict_null_checked"}},
					{Name: parser.Identifier{Text: "b"}, Type: parser.TypeName{Name: "text"}},
				})
				if err != nil {
					t.Fatal("anonymous FROM shape completion", err)
				}
				next := completed.(functions.JSONRecord)
				if !next.Defined || next.Info.RoutineReference != captured || next.SourcePosition() != call.SourcePosition() || next.Plan.BaseIndex() != tc.base || !reflect.DeepEqual(next.Args, call.Args) || next.Plan.ResultUsage() != next.ResultUsage() || next.ResultUsage().Type.CompositeLen() != 2 || !next.Properties().DomainDependent || !next.Properties().RuntimeDomainConversion || next.Properties().Volatility != expr.Stable || next.Properties().StageDependent != tc.set {
					t.Fatal("completion erased original authority or domain/stage/shape", next)
				}
				rebuilt, err := next.Reconstruct(next.Info, next.Args)
				if err != nil || !reflect.DeepEqual(rebuilt, next) {
					t.Fatal("reconstruct changed completed carrier", err)
				}
			}
			// Use real source/project admission, not scalar-planning's SRF skip.
			if err := admitRoutineCall(&evaluation, call, false, false); err != nil {
				t.Fatal("original source/project admission", err)
			}
			missing := make([]catalog.ExpressionDependency, 0, len(original))
			for _, dependency := range original {
				if dependency.Ref.Address != address {
					missing = append(missing, dependency)
				}
			}
			for _, manifest := range [][]catalog.ExpressionDependency{nil, {}, missing} {
				probe := evaluation
				probe.routineChecks = manifest
				if err := admitRoutineCall(&probe, call, false, false); err == nil {
					t.Fatal("missing original authority repaired from equal OID")
				}
			}
			for _, epoch := range []bool{false, true} {
				stale := append([]catalog.ExpressionDependency(nil), original...)
				for i := range stale {
					if stale[i].Ref.Address == address {
						if epoch {
							stale[i].Ref.Epoch++
						} else {
							stale[i].Revision++
						}
					}
				}
				probe := evaluation
				probe.routineChecks = stale
				if err := admitRoutineCall(&probe, call, false, false); err == nil {
					t.Fatal("stale epoch/revision admitted")
				}
			}
			probe := evaluation
			probe.routineAuthority = foreign.routineAuthority
			if err := admitRoutineCall(&probe, call, false, false); err == nil {
				t.Fatal("foreign equal-OID authority admitted")
			}
			remapped, _, err := retagBoundTypes(other.CatalogRead(), call, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == nil || old.IsDeferredRecord() {
					return old
				}
				current, _ := other.CatalogRead().LookupType(old.ID())
				return current
			})
			if err != nil {
				t.Fatal("actual foreign JSON carrier retag", err)
			}
			mapped := remapped.(functions.JSONRecord)
			if mapped.Plan.BaseIndex() != tc.base || len(mapped.Args) != arity || mapped.Defined != call.Defined || mapped.Properties() != call.Properties() || mapped.SourcePosition() != call.SourcePosition() {
				t.Fatal("foreign retag changed physical call contract", mapped)
			}
			for _, manifest := range [][]catalog.ExpressionDependency{original, nil, {}} {
				probe.routineChecks = manifest
				if err := admitRoutineCall(&probe, mapped, false, false); err == nil {
					t.Fatal("foreign retag manufactured original destination manifest")
				}
			}
			changed := *evaluation.routineAuthority
			changed.Catalog = changed.Catalog.WithState(catalog.CloneState(changed.Catalog.State()))
			replacement := *captured
			replacement.Strict = !captured.Strict
			changed.Catalog.State().SetDescriptor(address, &replacement)
			probe = evaluation
			probe.routineAuthority = &changed
			if err := admitRoutineCall(&probe, call, false, false); err == nil {
				t.Fatal("incompatible declaration replacement admitted")
			}
			invalid := call
			invalid.Info.RoutineReference = (*catalog.Procedure)(nil)
			if err := admitRoutineCall(&evaluation, invalid, false, false); err == nil {
				t.Fatal("typed nil routine carrier admitted")
			}
			if !reflect.DeepEqual(call, before) || !reflect.DeepEqual(binder.dependencies, original) {
				t.Fatal("completion/probes mutated original node or manifest")
			}
			if err := admitRoutineCall(&evaluation, call, false, false); err != nil {
				t.Fatal("original authority changed after negative probes", err)
			}
		})
	}
	t.Run("stored_recipe", func(t *testing.T) {
		view := e.CatalogRead()
		ref := roleTableRef(t, e, "json_authority_recipe")
		recipe := view.MustRelation(ref.Address)
		node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "json_to_record('{}'::json)")
		original := append([]catalog.ExpressionDependency(nil), binder.dependencies...)
		call := node.(functions.JSONRecord)
		identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
		grafted, _, err := mapBoundExpression(view, call, identity, nil, recipe, nil)
		if err != nil {
			t.Fatal("graft actual stored recipe", err)
		}
		// Grafting deliberately annotates descendants with the owning recipe.
		// Compare that exact metadata change, not grafted Args to ungrafted Args.
		expectedGraft := call
		expectedGraft.Info.RoutineRecipeReference = recipe
		expectedGraft.Args = append([]expr.Expr(nil), call.Args...)
		for i, arg := range expectedGraft.Args {
			literal, ok := arg.(plan.LiteralExpr)
			if !ok {
				t.Fatal("fixture JSON argument is not the original literal", arg)
			}
			literal.RoutineRecipeReference = recipe
			expectedGraft.Args[i] = literal
		}
		if !reflect.DeepEqual(grafted, expectedGraft) {
			t.Fatal("graft changed more than original node/child recipe provenance", grafted)
		}
		raw := statement(t, "SELECT json_to_record('{}'::json)").(*parser.Select).Targets[0].(parser.FunctionExpr)
		completed, err := binder.completeFunctionDefinitions(raw, grafted, []parser.FunctionColumnDefinition{
			{Name: parser.Identifier{Text: "a"}, Type: parser.TypeName{Name: "integer"}},
			{Name: parser.Identifier{Text: "b"}, Type: parser.TypeName{Name: "text"}},
		})
		if err != nil {
			t.Fatal("complete grafted shape", err)
		}
		finished := completed.(functions.JSONRecord)
		shape := finished.ResultUsage().Type
		if !finished.Defined || !shape.IsComposite() || shape.CompositeLen() != 2 || shape.CompositeMember(0).Name != "a" || shape.CompositeMember(0).Usage.Type.ID() != sqltype.Int4 || shape.CompositeMember(1).Name != "b" || shape.CompositeMember(1).Usage.Type.ID() != sqltype.Text {
			t.Fatal("genuine completed stored occurrence shape", finished)
		}
		rebuilt, err := finished.Reconstruct(finished.Info, finished.Args)
		if err != nil {
			t.Fatal("reconstruct grafted shape", err)
		}
		retagged, _, err := retagBoundTypes(view, rebuilt, identity)
		if err != nil {
			t.Fatal("retag grafted shape", err)
		}
		for i, retained := range []expr.Expr{grafted, finished, rebuilt, retagged} {
			next := retained.(functions.JSONRecord)
			expectedInfo := expectedGraft.Info
			expectedDefined := call.Defined
			if i != 0 {
				expectedInfo.Usage = finished.ResultUsage()
				expectedDefined = true
			}
			if next.Info != expectedInfo || next.Defined != expectedDefined || next.Plan.Name() != call.Plan.Name() || next.Plan.ResultUsage() != next.ResultUsage() || next.Plan.BaseIndex() != -1 || !reflect.DeepEqual(next.Args, expectedGraft.Args) {
				t.Fatal("completion/reconstruction/retag lost original routine/recipe/physical call", next)
			}
			if err := admitRoutineCall(&evaluation, retained, false, false); err != nil {
				t.Fatal("actual original stored recipe admission", err)
			}
		}
		if call.Info.RoutineRecipeReference != nil || call.Defined || !call.ResultUsage().Type.IsDeferredRecord() || !reflect.DeepEqual(binder.dependencies, original) {
			t.Fatal("graft/completion changed original node or manifest")
		}
	})
}

func TestJSONRecordRoutineFailedConstructor(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.clause.aggregateClause = "FROM"
	// A retained physical usage with an invalid integer typmod reaches the
	// genuine constructor's validation; no alternate provider or fake call is used.
	usage := b.typ(sqltype.Int4).Usage()
	usage.Typmod = 0
	column := catalog.Column{Name: "base", Usage: usage}
	b.query.namespace = &queryNamespace{columns: []queryColumn{{descriptor: column, slot: 0, leaf: -1, physical: -1}}, frame: []catalog.Column{column}}
	raw := statement(t, "SELECT json_populate_record(base,'{}'::json)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	_, err := b.bindFunctionSource(raw)
	if err == nil || err.Error() != "invalid JSON record plan" {
		t.Fatal("original constructor error not propagated", err)
	}
	for _, dependency := range b.dependencies {
		if dependency.Ref.Address == (ids.ObjectAddress{ids.ProcedureClassID, 3960, 0}) {
			t.Fatal("failed constructor captured the unselected outer declaration", dependency)
		}
	}
}
