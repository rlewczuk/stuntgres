package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func jsonbRecordRoutineUnitFixture(t *testing.T) (*Engine, *Operation, *StatementScope) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, query := range []string{
		"CREATE TABLE strict_null_rows(v integer)",
		"CREATE DOMAIN strict_null_checked AS integer CONSTRAINT strict_null_present CHECK(VALUE IS NOT NULL)",
		"CREATE VIEW jsonb_authority_recipe AS SELECT * FROM jsonb_to_record('{}'::jsonb) AS j(a integer,b text)",
	} {
		roleExecute(t, e, op, query)
	}
	roleAttributeCreate(t, e, op, "jsonb_authority_owner", false, parser.RoleOptions{Present: parser.RoleLogin, Login: true})
	roleAttributeCreate(t, e, op, "jsonb_authority_reader", false, parser.RoleOptions{Present: parser.RoleLogin, Login: true})
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

// Public conversion and ACL answers belong to the shared reference scenario.
// These bind genuine carriers and exercise their original owning lifetimes.
func TestJSONBRecordRoutineOriginalAuthority(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	other, otherOp, otherScope := jsonbRecordRoutineUnitFixture(t)
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	for _, tc := range []struct {
		name, expression, signature             string
		oid, result                             ids.OID
		strict, set, anonymous, populate, valid bool
	}{
		{"populate", "jsonb_populate_record(NULL::strict_null_rows,'{}'::jsonb)", "2283,3802", 3209, 2283, false, false, false, true, false},
		{"populate_set", "jsonb_populate_recordset(NULL::strict_null_rows,'[]'::jsonb)", "2283,3802", 3475, 2283, false, true, false, true, false},
		{"to_record", "jsonb_to_record(NULL::jsonb)", "3802", 3490, 2249, true, false, true, false, false},
		{"to_recordset", "jsonb_to_recordset('[]'::jsonb)", "3802", 3491, 2249, false, true, true, false, false},
		{"valid", "jsonb_populate_record_valid(NULL::strict_null_rows,NULL::jsonb)", "2283,3802", 6338, 16, false, false, false, true, true},
		{"populate_anonymous", "jsonb_populate_record(NULL::record,'{}'::jsonb)", "2283,3802", 3209, 2283, false, false, true, true, false},
		{"populate_set_anonymous", "jsonb_populate_recordset(NULL::record,'[]'::jsonb)", "2283,3802", 3475, 2283, false, true, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
			call, ok := node.(functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties])
			if !ok {
				t.Fatal("original JSONB carrier", node)
			}
			arity := 1
			if tc.populate {
				arity = 2
			}
			if len(call.Args) != arity || call.Definitions || call.Unresolved != tc.anonymous || call.ReturnsSet() != tc.set || isQuerySet(call) != tc.set || !validFunctionSourceCall(call) || binder.queryHasTargetSet != tc.set {
				t.Fatal("physical/source/project contract", call)
			}
			if call.Properties().Volatility != Stable || call.Properties().StageDependent != tc.set || !call.Properties().RuntimeDomainConversion || call.Properties().DomainDependent {
				t.Fatal("stable/domain/stage properties", call.Properties())
			}
			if call.Args[arity-1].ResultUsage().Type.ID() != sqltype.JSONB {
				t.Fatal("lost JSONB input")
			}
			if tc.populate && call.RecordUsage() != call.Args[0].ResultUsage() {
				t.Fatal("lost physical base usage")
			}
			if tc.valid {
				if call.ResultUsage().Type.ID() != sqltype.Bool || call.RecordUsage() == call.ResultUsage() {
					t.Fatal("valid must retain real base and BOOL result")
				}
			}
			captured, err := expressionRoutineDescriptor(call)
			if err != nil || captured == nil || captured.builtin == nil {
				t.Fatal("missing original descriptor", captured, err)
			}
			address := ids.ObjectAddress{ProcedureClassID, tc.oid, 0}
			d := captured.builtin
			if captured.address != address || captured.namespace != (ids.ObjectAddress{NamespaceClassID, 11, 0}) || captured.name != call.Name || string(captured.signature) != tc.signature || captured.result != tc.result || captured.routineKind != functionRoutine || captured.strict != tc.strict || captured.volatility != Stable || captured.revision == 0 || len(captured.body) != 0 {
				t.Fatal("released identity", captured)
			}
			if d.kind != functionRoutine || d.result != tc.result || d.strict != tc.strict || d.volatility != Stable || d.parallel != 's' || d.returnsSet != tc.set || d.variadic != 0 || d.defaults != 0 || d.providers&jsonRoutineProvider == 0 {
				t.Fatal("released full declaration", d)
			}
			if evaluation.catalog.state.descriptors[address] != captured {
				t.Fatal("not the borrowed owned descriptor")
			}
			original := append([]expressionDependency(nil), binder.dependencies...)
			selected := 0
			for _, dependency := range original {
				if dependency.ref.Address == address {
					selected++
					if dependency.ref.Domain() != e.domain || dependency.ref.Epoch != evaluation.catalog.Epoch() || dependency.revision != captured.revision {
						t.Fatal("original ref/epoch/revision", dependency)
					}
				} else if dependency.ref.Address.ClassID == ProcedureClassID {
					t.Fatal("implicit codec/helper EXECUTE edge", dependency)
				}
			}
			if selected != 1 {
				t.Fatal("original selected manifest cardinality", selected)
			}
			before := call
			before.Args = append([]BoundExpr(nil), call.Args...)
			rebuilt, err := call.Reconstruct(call.Info, call.Args)
			if err != nil || !reflect.DeepEqual(rebuilt, call) {
				t.Fatal("reconstruct lost physical metadata", err)
			}
			if tc.valid {
				raw := statement(t, "SELECT "+tc.expression).(*parser.Select).Targets[0].(parser.FunctionExpr)
				if _, err := binder.completeFunctionDefinitions(raw, call, []parser.FunctionColumnDefinition{
					{Name: parser.Identifier{Text: "a"}, Type: parser.TypeName{Name: "integer"}},
				}); err == nil {
					t.Fatal("BOOL validity accepted a fake record definition")
				}
			}
			if tc.anonymous {
				raw := statement(t, "SELECT "+tc.expression).(*parser.Select).Targets[0].(parser.FunctionExpr)
				_, sourceBinder, _ := strictNullUnitBind(t, e, op, scope, tc.expression)
				sourceBinder.aggregateClause = "FROM"
				sourceBinder.queryNamespace = &queryNamespace{}
				e.mu.Lock()
				source, sourceErr := sourceBinder.bindFunctionSource(raw)
				e.mu.Unlock()
				if sourceErr != nil {
					t.Fatal("actual FROM binding", sourceErr)
				}
				completed, err := sourceBinder.completeFunctionDefinitions(raw, source, []parser.FunctionColumnDefinition{
					{Name: parser.Identifier{Text: "a"}, Type: parser.TypeName{Name: "strict_null_checked"}},
					{Name: parser.Identifier{Text: "b"}, Type: parser.TypeName{Name: "text"}},
				})
				if err != nil {
					t.Fatal("actual FROM completion", err)
				}
				next := completed.(functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties])
				if !next.Definitions || next.Unresolved || next.Info.RoutineReference != captured || next.SourcePosition() != call.SourcePosition() || next.Name != call.Name || !reflect.DeepEqual(next.Args, call.Args) || next.RecordUsage() != next.ResultUsage() || next.ResultUsage().Type.CompositeLen() != 2 || !next.Properties().DomainDependent || !next.Properties().RuntimeDomainConversion || next.Properties().Volatility != Stable || next.Properties().StageDependent != tc.set {
					t.Fatal("completion lost authority/layout/properties", next)
				}
				reconstructed, err := next.Reconstruct(next.Info, next.Args)
				if err != nil || !reflect.DeepEqual(reconstructed, next) {
					t.Fatal("completed reconstruction", err)
				}
				if err := admitRoutineCall(&evaluation, next, false, false); err != nil {
					t.Fatal("completed occurrence admission", err)
				}
			}
			// false,false is the actual source/project demand stage, not the
			// scalar planner's intentional SETOF skip. RECORD strictness and
			// valid's nonstrict BOOL must not erase even all-NULL calls here.
			if err := admitRoutineCall(&evaluation, call, false, false); err != nil {
				t.Fatal("original admission", err)
			}
			missing := make([]expressionDependency, 0, len(original))
			for _, dep := range original {
				if dep.ref.Address != address {
					missing = append(missing, dep)
				}
			}
			for _, manifest := range [][]expressionDependency{nil, {}, missing} {
				probe := evaluation
				probe.routineChecks = manifest
				if err := admitRoutineCall(&probe, call, false, false); err == nil {
					t.Fatal("missing owning manifest rescued by OID")
				}
			}
			for _, mutation := range []string{"epoch", "revision", "foreign_ref"} {
				copied := append([]expressionDependency(nil), original...)
				for i := range copied {
					if copied[i].ref.Address == address {
						switch mutation {
						case "epoch":
							copied[i].ref.Epoch++
						case "revision":
							copied[i].revision++
						case "foreign_ref":
							copied[i].ref = other.domain.Ref(copied[i].ref.Epoch, copied[i].ref.Address)
						}
					}
				}
				probe := evaluation
				probe.routineChecks = copied
				if err := admitRoutineCall(&probe, call, false, false); err == nil {
					t.Fatal("invalid original manifest admitted", mutation)
				}
			}
			probe := evaluation
			probe.routineChecks = append([]expressionDependency(nil), original...)
			if err := admitRoutineCall(&probe, call, false, false); err != nil {
				t.Fatal("full original ref copy lost authority", err)
			}
			probe.routineAuthority = foreign.routineAuthority
			if err := admitRoutineCall(&probe, call, false, false); err == nil {
				t.Fatal("foreign same-OID authority admitted")
			}
			mapped, _, err := retagBoundTypes(other.CatalogRead(), call, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == nil || old.IsDeferredRecord() {
					return old
				}
				current, _ := other.CatalogRead().LookupType(old.ID())
				return current
			})
			if err != nil {
				t.Fatal("actual foreign retag", err)
			}
			retagged := mapped.(functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties])
			if retagged.Name != call.Name || len(retagged.Args) != len(call.Args) || retagged.Definitions != call.Definitions || retagged.Unresolved != call.Unresolved || retagged.SourcePosition() != call.SourcePosition() || retagged.Properties() != call.Properties() {
				t.Fatal("retag changed physical carrier contract", retagged)
			}
			for _, manifest := range [][]expressionDependency{original, nil, {}} {
				probe.routineChecks = manifest
				if err := admitRoutineCall(&probe, mapped, false, false); err == nil {
					t.Fatal("retag manufactured destination manifest")
				}
			}
			changed := *evaluation.routineAuthority
			changed.catalog.state = cloneCatalog(changed.catalog.state)
			replacement := *captured
			replacement.strict = !captured.strict
			changed.catalog.state.descriptors[address] = &replacement
			probe = evaluation
			probe.routineAuthority = &changed
			if err := admitRoutineCall(&probe, call, false, false); err == nil {
				t.Fatal("incompatible declaration admitted")
			}
			invalid := call
			invalid.Info.RoutineReference = (*procedureDescriptor)(nil)
			if err := admitRoutineCall(&evaluation, invalid, false, false); err == nil {
				t.Fatal("typed nil carrier admitted")
			}
			if !reflect.DeepEqual(call, before) || !reflect.DeepEqual(binder.dependencies, original) {
				t.Fatal("probes mutated original graph/manifest")
			}
			if err := admitRoutineCall(&evaluation, call, false, false); err != nil {
				t.Fatal("original admission lost", err)
			}
		})
	}
}

func TestJSONBRecordRoutineStoredRecipe(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "jsonb_authority_recipe")
	recipe := view.state.descriptors[ref.Address].(*relationDescriptor)
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "jsonb_to_record('{}'::jsonb)")
	original := append([]expressionDependency(nil), binder.dependencies...)
	call := node.(functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties])
	captured, err := expressionRoutineDescriptor(call)
	if err != nil || captured == nil {
		t.Fatal("original stored-call descriptor", err)
	}
	owner, revision := captured.owner, captured.revision
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	grafted, _, err := mapBoundExpression(view, call, identity, nil, recipe, nil)
	if err != nil {
		t.Fatal("graft actual stored recipe", err)
	}
	raw := statement(t, "SELECT jsonb_to_record('{}'::jsonb)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	completed, err := binder.completeFunctionDefinitions(raw, grafted, []parser.FunctionColumnDefinition{
		{Name: parser.Identifier{Text: "a"}, Type: parser.TypeName{Name: "integer"}},
		{Name: parser.Identifier{Text: "b"}, Type: parser.TypeName{Name: "text"}},
	})
	if err != nil {
		t.Fatal("complete grafted occurrence", err)
	}
	finished := completed.(functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties])
	shape := finished.ResultUsage().Type
	if !shape.IsComposite() || shape.CompositeLen() != 2 || shape.CompositeMember(0).Usage.Type.ID() != sqltype.Int4 || shape.CompositeMember(1).Usage.Type.ID() != sqltype.Text {
		t.Fatal("actual completed layout")
	}
	rebuilt, err := finished.Reconstruct(finished.Info, finished.Args)
	if err != nil {
		t.Fatal(err)
	}
	retagged, _, err := retagBoundTypes(view, rebuilt, identity)
	if err != nil {
		t.Fatal(err)
	}
	for i, retained := range []BoundExpr{grafted, finished, rebuilt, retagged} {
		next := retained.(functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties])
		if next.Info.RoutineReference != captured || next.Info.RoutineRecipeReference != recipe || next.SourcePosition() != call.SourcePosition() || next.Definitions != (i != 0) || next.Unresolved != (i == 0) || next.Name != call.Name || len(next.Args) != len(call.Args) || next.Args[0].ResultUsage() != call.Args[0].ResultUsage() {
			t.Fatal("stored chain lost original authority/arguments", next)
		}
		childRecipe, err := expressionRecipeDescriptor(next.Args[0])
		if err != nil || childRecipe != recipe {
			t.Fatal("stored child lost owning recipe", err)
		}
		if err := admitRoutineCall(&evaluation, retained, false, false); err != nil {
			t.Fatal("stored original manifest admission", err)
		}
		missing := make([]expressionDependency, 0, len(recipe.view.dependencies))
		for _, dependency := range recipe.view.dependencies {
			if dependency.ref.Address != captured.address {
				missing = append(missing, dependency)
			}
		}
		for _, manifest := range [][]expressionDependency{nil, {}, missing} {
			probe := evaluation
			// Select the genuine owning recipe just as normal admission does,
			// then remove its own selected-call authority. Do not allow a
			// recipe reselection to replace the deliberately absent manifest.
			if err := probe.selectExpressionRecipe(recipe); err != nil {
				t.Fatal("select stored owning recipe", err)
			}
			probe.routineChecks = manifest
			if err := admitRoutineCall(&probe, retained, false, false); err == nil {
				t.Fatal("stored chain rescued missing owning manifest")
			}
		}
	}
	if call.Info.RoutineReference != captured || call.Info.RoutineRecipeReference != nil || call.Definitions || !call.Unresolved || captured.owner != owner || captured.revision != revision || view.state.descriptors[captured.address] != captured || !reflect.DeepEqual(evaluation.routineChecks, original) {
		t.Fatal("stored chain mutated original")
	}
	if childRecipe, err := expressionRecipeDescriptor(call.Args[0]); err != nil || childRecipe != nil {
		t.Fatal("stored chain mutated original child ancestry", err)
	}
	if err := admitRoutineCall(&evaluation, call, false, false); err != nil {
		t.Fatal("original captured manifest lost admission", err)
	}
}

func TestJSONBRecordRoutineEndedScope(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	var nodes []BoundExpr
	var evaluations []EvalContext
	for _, expression := range []string{
		"jsonb_populate_record(NULL::strict_null_rows,NULL::jsonb)",
		"jsonb_populate_recordset(NULL::strict_null_rows,NULL::jsonb)",
		"jsonb_to_record(NULL::jsonb)",
		"jsonb_to_recordset(NULL::jsonb)",
		"jsonb_populate_record_valid(NULL::strict_null_rows,NULL::jsonb)",
	} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, expression)
		scope.routineAuth = *evaluation.routineAuthority
		evaluation.routineAuthority = nil
		if err := admitRoutineCall(&evaluation, node, false, false); err != nil {
			t.Fatal("live owning scope", err)
		}
		nodes = append(nodes, node)
		evaluations = append(evaluations, evaluation)
	}
	scope.Finish(nil)
	for i, node := range nodes {
		if err := admitRoutineCall(&evaluations[i], node, false, false); err == nil {
			t.Fatal("ended owning scope rescued by descriptor")
		}
	}
}

func eMustJSONBRoutineAuthority(t *testing.T, e *Engine, op *Operation) *AuthorizationView {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	return &auth
}

func TestJSONBRecordRoutineFailedConstructor(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "FROM"
	usage := b.typ(sqltype.Int4).Usage()
	usage.Typmod = 0
	column := ColumnDescriptor{Name: "base", Usage: usage}
	b.queryNamespace = &queryNamespace{columns: []queryColumn{{descriptor: column, slot: 0, leaf: -1, physical: -1}}, frame: []ColumnDescriptor{column}}
	raw := statement(t, "SELECT jsonb_populate_record(base,'{}'::jsonb)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	_, err := b.bindFunctionSource(raw)
	if err == nil {
		t.Fatal("real constructor failure", err)
	}
	for _, dependency := range b.dependencies {
		if dependency.ref.Address == (ids.ObjectAddress{ProcedureClassID, 3209, 0}) {
			t.Fatal("failed constructor captured outer routine", dependency)
		}
	}
}

// Owner transfer changes the definition revision. Declaration compatibility is
// not permission to rescue an old graph from its stale original manifest.
func TestJSONBRecordRoutineOwnershipCOW(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	other := New()
	foreignOwner := other.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	for _, expression := range []string{
		"jsonb_populate_record(NULL::strict_null_rows,'{}'::jsonb)",
		"jsonb_populate_recordset(NULL::strict_null_rows,'[]'::jsonb)",
		"jsonb_to_record('{}'::jsonb)",
		"jsonb_to_recordset('[]'::jsonb)",
		"jsonb_populate_record_valid(NULL::strict_null_rows,'{}'::jsonb)",
	} {
		node, binder, evaluation := strictNullUnitBind(t, e, op, scope, expression)
		captured, err := expressionRoutineDescriptor(node)
		if err != nil {
			t.Fatal(err)
		}
		original := append([]expressionDependency(nil), binder.dependencies...)
		base := e.CatalogRead()
		// Reusing a live, same-domain principal is a genuine owner-COW, even
		// though the SQL surface does not offer ALTER FUNCTION OWNER here.
		target := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("jsonb_authority_owner")
		stage := e.BeginCatalogStage(base)
		if err := stageOwnership(stage, captured, target); err != nil {
			t.Fatal(err)
		}
		ownershipUnitPublish(t, e, stage)
		e.DiscardCatalogStage(stage)
		current := e.CatalogRead().state.descriptors[captured.address].(*procedureDescriptor)
		if current == captured || current.owner != target || current.revision == captured.revision || base.state.descriptors[captured.address] != captured {
			t.Fatal("owner COW/source immutability")
		}
		auth := eMustJSONBRoutineAuthority(t, e, op)
		ownerOp, ownerDone := roleOperation(t, e, "jsonb_authority_owner")
		ownerAuth := eMustJSONBRoutineAuthority(t, e, ownerOp)
		ownerDone()
		if got, err := validateRoutineDescriptor(auth.catalog, auth.roles, captured); err != nil || got != current {
			t.Fatal("compatible owner declaration", err)
		}
		for _, dep := range original {
			if dep.ref.Address == captured.address {
				if err := ownerAuth.Check(AccessRequirement{Object: dep.ref, Privileges: authorization.ExecutePrivilege}); err != nil {
					t.Fatal("fresh current-owner permission", err)
				}
			}
		}
		if err := admitRoutineCall(&evaluation, node, false, false); err != nil {
			t.Fatal("retained original snapshot admission", err)
		}
		probe := evaluation
		probe.routineAuthority = auth
		if err := admitRoutineCall(&probe, node, false, false); err == nil {
			t.Fatal("owner revision rescued old graph")
		}
		fresh, freshBinder, freshEvaluation := strictNullUnitBind(t, e, op, scope, expression)
		freshDescriptor, err := expressionRoutineDescriptor(fresh)
		if err != nil || freshDescriptor != current {
			t.Fatal("fresh analysis did not capture owner/revision", err)
		}
		freshEvaluation.routineAuthority = ownerAuth
		if err := admitRoutineCall(&freshEvaluation, fresh, false, false); err != nil {
			t.Fatal("fresh current admission", err)
		}
		found := false
		for _, dep := range freshBinder.dependencies {
			if dep.ref.Address == current.address {
				found = dep.revision == current.revision && dep.ref.Domain() == e.domain
			}
		}
		if !found {
			t.Fatal("fresh original manifest")
		}
		// Foreign and zero owners travel through the same actual COW seam.
		// Validate the staged carrier against genuine authority; never forge a
		// raw descriptor owner or manufacture a destination manifest.
		for _, owner := range []authorization.PrincipalRef{{}, foreignOwner} {
			badStage := e.BeginCatalogStage(e.CatalogRead())
			if err := stageOwnership(badStage, current, owner); err != nil {
				t.Fatal(err)
			}
			bad := badStage.descriptors[current.address].(*procedureDescriptor)
			if _, err := validateRoutineDescriptor(auth.catalog, auth.roles, bad); err == nil {
				t.Fatal("zero/foreign staged owner provenance accepted")
			}
			if _, err := e.ValidateCatalogStage(context.Background(), badStage); err == nil {
				t.Fatal("zero/foreign owner publication accepted")
			}
			e.DiscardCatalogStage(badStage)
		}
		if !reflect.DeepEqual(binder.dependencies, original) || captured.owner == target {
			t.Fatal("owner probes mutated original authority")
		}
	}
}

// ACL-only COW keeps the definition revision. The same original graph and
// complete ObjectRef remain admissible, but permissions come from each fresh
// authorization view, never from the borrowed descriptor's old ACL.
func TestJSONBRecordRoutineACLCOW(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	readerOp, readerDone := roleOperation(t, e, "jsonb_authority_reader")
	defer readerDone()
	reader := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("jsonb_authority_reader")
	for _, expression := range []string{
		"jsonb_populate_record(NULL::strict_null_rows,NULL::jsonb)",
		"jsonb_populate_recordset(NULL::strict_null_rows,NULL::jsonb)",
		"jsonb_to_record(NULL::jsonb)",
		"jsonb_to_recordset(NULL::jsonb)",
		"jsonb_populate_record_valid(NULL::strict_null_rows,NULL::jsonb)",
	} {
		node, binder, evaluation := strictNullUnitBind(t, e, op, scope, expression)
		captured, err := expressionRoutineDescriptor(node)
		if err != nil {
			t.Fatal(err)
		}
		original := append([]expressionDependency(nil), binder.dependencies...)
		var ref ids.ObjectRef
		for _, dependency := range original {
			if dependency.ref.Address == captured.address {
				ref = dependency.ref
			}
		}
		retained := evaluation
		retained.routineAuthority = eMustJSONBRoutineAuthority(t, e, readerOp)
		if err := admitRoutineCall(&retained, node, false, false); err != nil {
			t.Fatal("original PUBLIC-default authority", err)
		}
		for _, allowed := range []bool{false, true, false} {
			acl := authorization.ACLValue{Explicit: true}
			if allowed {
				acl.Entries = []authorization.ACLEntry{
					objectACLUnitEntry(captured.owner, reader, authorization.ExecutePrivilege, 0),
				}
			}
			if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
				t.Fatal(err)
			}
			current := e.CatalogRead().state.descriptors[ref.Address].(*procedureDescriptor)
			auth := eMustJSONBRoutineAuthority(t, e, readerOp)
			if current == captured || current.revision != captured.revision {
				t.Fatal("ACL COW changed original definition identity")
			}
			if got, err := validateRoutineDescriptor(auth.catalog, auth.roles, captured); err != nil || got != current {
				t.Fatal("ACL COW declaration compatibility", err)
			}
			probe := evaluation
			probe.routineAuthority = auth
			err := admitRoutineCall(&probe, node, false, false)
			if (err == nil) != allowed {
				t.Fatal("current ACL not used by original graph", allowed, err)
			}
			if err := admitRoutineCall(&retained, node, false, false); err != nil {
				t.Fatal("COW mutated original authority snapshot", err)
			}
		}
		if !reflect.DeepEqual(binder.dependencies, original) {
			t.Fatal("ACL COW changed original manifest")
		}
		if selected, err := expressionRoutineDescriptor(node); err != nil || selected != captured {
			t.Fatal("ACL COW replaced original carrier", err)
		}
	}
}
