package binder_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestDblinkUpdateStoredCarrier(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION dblink")
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dblink"}))
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := enginetest.FunctionCall("dblink_build_sql_update", enginetest.StringLiteral("absent_relation"), enginetest.TypedLiteral("int2vector", "1 2"), enginetest.TypedLiteral("integer", "2"), enginetest.StringLiteral("{a,b}"), enginetest.StringLiteral("{remote,keys}"))
	bound, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := bound.(plan.InstalledDblinkBuildSQLUpdateExpr)
	if !ok {
		t.Fatalf("not an authenticated carrier: %T", bound)
	}
	if n.Procedure != view.Get(ext.Members[2]) || n.CallRoutineReference() != n.Procedure || n.Ref.Address != ext.Members[2] || n.Ref.Epoch != view.Epoch() || n.Ref.Domain() != view.Owner().Domain || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent {
		t.Fatal("lost retained authority", n)
	}
	if err := stored.ValidateInstalledDblinkBuildSQLUpdate(n, view); err != nil {
		t.Fatal(err)
	}
	if !analyze.ClosedPgstattupleDefault(n, view) || !analyze.ClosedPgstattupleCheck(n, view) {
		t.Fatal("genuine installed UPDATE lost stored closure")
	}
	var rendered []ids.ObjectRef
	canonical, err := (stored.ScalarRenderer{Dependencies: &rendered}).SQL(context.Background(), view, n)
	if err != nil || !strings.Contains(canonical, `"public"."dblink_build_sql_update"(`) {
		t.Fatal("closed scalar renderer lost installed routine", canonical, err)
	}
	foundRoutine := false
	for _, dependency := range rendered {
		foundRoutine = foundRoutine || dependency == n.Ref
		if dependency.Address.ClassID == ids.RelationClassID {
			t.Fatal("scalar renderer captured TEXT relation lookup")
		}
	}
	if !foundRoutine {
		t.Fatal("stored renderer omitted genuine routine dependency")
	}
	cloned, err := stored.RemapExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || !reflect.DeepEqual(cloned, n) || cloned.(plan.InstalledDblinkBuildSQLUpdateExpr).Procedure != n.Procedure {
		t.Fatal("identity clone lost immutable carrier", cloned, err)
	}
	var walked []expr.Expr
	expr.Walk(n, plan.ExpressionChildren, func(child expr.Expr) { walked = append(walked, child) })
	if len(walked) != 6 || !reflect.DeepEqual(walked[0], n) {
		t.Fatal("walk lost installed scalar or its five owned operands", walked)
	}
	for i := range n.Args {
		if !reflect.DeepEqual(walked[i+1], n.Args[i]) {
			t.Fatal("walk reordered lexical operands", i)
		}
	}
	children := n.Children()
	if len(children) != 5 {
		t.Fatal("lost lexical operands")
	}
	capture, admitted, err := n.Capture(expr.CaptureFunction)
	if err != nil || !admitted || capture.Count != 5 || !reflect.DeepEqual(capture.Args, children) {
		t.Fatal("lost ordered capture operands", capture, admitted, err)
	}
	for i := range n.Args {
		if !reflect.DeepEqual(children[i], n.Args[i]) {
			t.Fatal("reordered source operands", i)
		}
	}
	for _, reconstruct := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
		copy, err := reconstruct(expr.InfoOf(n), children)
		if err != nil {
			t.Fatal(err)
		}
		fresh := copy.(plan.InstalledDblinkBuildSQLUpdateExpr)
		if fresh.Ref != n.Ref || fresh.Procedure != n.Procedure || fresh.Info != n.Info || !reflect.DeepEqual(fresh.Args, n.Args) {
			t.Fatal("reconstruction lost identity or operands")
		}
		borrowed := fresh.Children()
		borrowed[0] = borrowed[1]
		if !reflect.DeepEqual(fresh.Args, n.Args) {
			t.Fatal("borrowed traversal slice mutated retained slots")
		}
	}
	badInfo := expr.InfoOf(n)
	badInfo.RoutineReference = nil
	if _, err := n.Rebuild(badInfo, children); err == nil {
		t.Fatal("rebuild lost hidden provider")
	}
	if _, err := n.Substitute(expr.InfoOf(n), children[:4]); err == nil {
		t.Fatal("substitute accepted missing operand")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*plan.InstalledDblinkBuildSQLUpdateExpr)
	}{
		{"missing_routine", func(n *plan.InstalledDblinkBuildSQLUpdateExpr) { n.RoutineReference = nil }},
		{"zero_epoch", func(n *plan.InstalledDblinkBuildSQLUpdateExpr) { n.Ref.Epoch = 0 }},
		{"future_epoch", func(n *plan.InstalledDblinkBuildSQLUpdateExpr) { n.Ref.Epoch++ }},
		{"wrong_address", func(n *plan.InstalledDblinkBuildSQLUpdateExpr) { n.Ref.Address.ObjectID++ }},
		{"wrong_argument", func(n *plan.InstalledDblinkBuildSQLUpdateExpr) { n.Args[1] = n.Args[0] }},
		{"wrong_result", func(n *plan.InstalledDblinkBuildSQLUpdateExpr) { n.Usage = n.Args[2].ResultUsage() }},
		{"same_signature_insert", func(n *plan.InstalledDblinkBuildSQLUpdateExpr) {
			n.Procedure = view.MustProcedure(ext.Members[1])
			n.RoutineReference = n.Procedure
			n.Ref = view.Owner().Domain.Ref(view.Epoch(), n.Procedure.Address())
		}},
		{"foreign_domain", func(n *plan.InstalledDblinkBuildSQLUpdateExpr) {
			n.Ref = engine.New().Catalog.Domain.Ref(n.Ref.Epoch, n.Ref.Address)
		}},
	} {
		bad := n
		tc.mutate(&bad)
		if stored.ValidateInstalledDblinkBuildSQLUpdate(bad, view) == nil {
			t.Fatal("forged retained recipe accepted", tc.name)
		}
		if analyze.ClosedPgstattupleDefault(bad, view) {
			t.Fatal("forged retained recipe acquired DEFAULT closure", tc.name)
		}
		if _, err := stored.DefaultSQL(context.Background(), view, bad); err == nil {
			t.Fatal("forged retained recipe rendered as DEFAULT", tc.name)
		}
	}
	staleState := catalog.CloneState(view.State())
	staleState.SetEpoch(view.Epoch() + 1)
	if stored.ValidateInstalledDblinkBuildSQLUpdate(n, e.Catalog.Read(staleState)) == nil {
		t.Fatal("stale retained epoch borrowed a surviving genuine declaration")
	}
	refs := stored.DependRefs{Context: context.Background(), Catalog: view}
	refs.Expression(n)
	if refs.Err != nil {
		t.Fatal(refs.Err)
	}
	if _, exists := refs.Addresses[n.Procedure.Address()]; !exists {
		t.Fatal("walker omitted hidden routine reference")
	}
	for address := range refs.Addresses {
		if address.ClassID == ids.RelationClassID {
			t.Fatal("TEXT relation argument acquired a permanent relation dependency")
		}
	}
	retagged, changed, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil || changed || !reflect.DeepEqual(retagged, n) {
		t.Fatal("unchanged retag changed immutable source", changed, err)
	}
	// Replace a real catalog type generation, then rebuild its dependent
	// companions through the production reownership boundary. Retagging only
	// TEXT would leave text[] pointing at the retired element generation.
	typedState := catalog.CloneState(view.State())
	textAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Text)}
	textOwner := *view.Get(textAddress).(*catalog.Type)
	textGeneration := *textOwner.SQLType
	textOwner.SQLType = &textGeneration
	typedState.SetDescriptor(textAddress, &textOwner)
	if changed, err := typedef.ReownDomainTypes(typedState, nil, false, nil); err != nil || !changed {
		t.Fatal("new element generation did not rebuild dependent types", changed, err)
	}
	typedView := e.Catalog.Read(typedState)
	arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(textOwner.Array)}
	arrayOwner := typedView.MustType(arrayAddress)
	elementOwner, err := catalog.ArrayTypeOwner(typedView, arrayOwner)
	if err != nil || elementOwner != &textOwner || arrayOwner.SQLType == n.Args[3].ResultUsage().Type || arrayOwner.SQLType.Element() != textOwner.SQLType || view.MustType(arrayAddress).SQLType != n.Args[3].ResultUsage().Type || view.MustType(arrayAddress).SQLType.Element() != n.ResultUsage().Type {
		t.Fatal("retag fixture lost current reciprocal array authority or mutated source", err)
	}
	retagged, changed, err = stored.RetagTypes(typedView, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		current, _ := typedView.LookupType(d.ID())
		return current
	})
	if err != nil || !changed {
		t.Fatal("new catalog generation did not retag carrier", changed, err)
	}
	fresh := retagged.(plan.InstalledDblinkBuildSQLUpdateExpr)
	if fresh.ResultUsage().Type != textOwner.SQLType || fresh.Args[0].ResultUsage().Type != textOwner.SQLType || fresh.Procedure != n.Procedure || fresh.Ref != n.Ref || fresh.RoutineReference != n.RoutineReference || n.ResultUsage().Type == textOwner.SQLType || !reflect.DeepEqual(n.Args, updateChildrenArray(children)) {
		t.Fatal("retag lost installed authority or mutated old graph")
	}
	for i, child := range fresh.Args {
		original := n.Args[i]
		expected, _ := typedView.LookupType(original.ResultUsage().Type.ID())
		before, after := expr.InfoOf(original), expr.InfoOf(child)
		before.Usage.Type, after.Usage.Type = nil, nil
		literal, ok := child.(plan.LiteralExpr)
		if !ok || child.ResultUsage().Type != expected || literal.Value.Type() != expected || !literal.Value.ValidResult(child.ResultUsage()) || before != after {
			t.Fatal("retag lost operand generation, typed value or provenance", i)
		}
		if expected.IsArray() {
			for j := range literal.Value.ArrayLen() {
				if literal.Value.ArrayElement(j).Type() != expected.Element() {
					t.Fatal("retag retained an old array element generation", i, j)
				}
			}
		}
	}
	beforeInfo, afterInfo := n.Info, fresh.Info
	beforeInfo.Usage.Type, afterInfo.Usage.Type = nil, nil
	if beforeInfo != afterInfo {
		t.Fatal("type generation retag changed source provenance")
	}
	if text, err := stored.DefaultSQL(context.Background(), typedView, fresh); err != nil || text != canonical {
		t.Fatal("retag changed canonical stored SQL", text, err)
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 950000}, n.Procedure.Address(): {ClassID: ids.ProcedureClassID, ObjectID: 950001}, ext.Members[0]: {ClassID: ids.ProcedureClassID, ObjectID: 950002}, ext.Members[1]: {ClassID: ids.ProcedureClassID, ObjectID: 950003}}
	state := catalog.CloneState(view.State())
	descriptor := extension.DescriptorRemapper(mapping)
	for old := range mapping {
		state.UnbindName(catalog.DescriptorNameKey(view.Get(old)))
		state.RemoveDescriptor(old)
	}
	for old, destination := range mapping {
		d := descriptor(view.Get(old))
		state.SetDescriptor(destination, d)
		state.BindName(catalog.DescriptorNameKey(d), destination)
	}
	state.SetEpoch(state.Epoch() + 1)
	remap := func(ref ids.ObjectRef) ids.ObjectRef {
		ref.Epoch = state.Epoch()
		if address, exists := mapping[ref.Address]; exists {
			ref.Address = address
		}
		return ref
	}
	mapper := stored.ViewQueryRemapper{Catalog: e.Catalog.Read(state), Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(descriptor, nil, nil)}
	query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "sql", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
	mappedQuery, err := mapper.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	mapped := mappedQuery.(*plan.ProjectNode).Expressions[0].(plan.InstalledDblinkBuildSQLUpdateExpr)
	if mapped.Ref != remap(n.Ref) || mapped.Procedure.Address() != mapping[n.Procedure.Address()] || mapped.CallRoutineReference() != mapped.Procedure || n.Procedure != view.Get(n.Ref.Address) || n.Ref.Epoch != view.Epoch() || !reflect.DeepEqual(n.Args, updateChildrenArray(children)) {
		t.Fatal("remap lost destination authority or mutated source")
	}
	if err := stored.ValidateInstalledDblinkBuildSQLUpdate(mapped, e.Catalog.Read(state)); err != nil {
		t.Fatal(err)
	}
	current := *mapped.Procedure
	state.UnbindName(catalog.DescriptorNameKey(&current))
	current.ObjectName = "update_renamed"
	state.SetDescriptor(current.Address(), &current)
	state.BindName(catalog.DescriptorNameKey(&current), current.Address())
	text, err := (stored.Deparser{View: e.Catalog.Read(state)}).SQL(context.Background(), mapped, stored.DeparseParent{})
	if err != nil || !strings.Contains(text, "update_renamed(") || strings.Contains(text, "dblink_build_sql_update(") {
		t.Fatal("renderer selected frozen C symbol instead of current SQL name", text, err)
	}
	// The genuine scalar retains runtime/fold dependence, but PL must not
	// confuse that property with a query-stage slot or add a SPI traceback.
	scalar := &plan.ProjectNode{Child: &plan.SingletonNode{}, Expressions: []expr.Expr{n}}
	if !n.Properties().StageDependent || !plan.PLSimpleExpression(scalar) {
		t.Fatal("installed scalar lost simple-expression execution provenance")
	}
	scalar.Expressions[0] = plan.PLAssignmentExpr{Info: n.Info, Child: n}
	if !plan.PLSimpleExpression(scalar) {
		t.Fatal("assignment wrapper confused inherited runtime dependence with SPI")
	}
	for _, stage := range []expr.Expr{plan.AggregateExpr{Info: n.Info}, plan.GroupExpr{Info: n.Info}, plan.GroupingExpr{Info: n.Info}, &plan.WindowExpr{Info: n.Info}, plan.CachedOrderExpr{Info: n.Info}} {
		scalar.Expressions[0] = stage
		if plan.PLSimpleExpression(scalar) {
			t.Fatal("query-stage slot admitted as a PL simple expression")
		}
	}
	t.Run("published_default_and_view", func(t *testing.T) {
		const call = "dblink_build_sql_update('absent_relation','1'::int2vector,1,'{a}'::text[],'{b}'::text[])"
		enginetest.RoleExecute(t, e, op, "CREATE TABLE update_stored_default(sql text DEFAULT "+call+")")
		enginetest.RoleExecute(t, e, op, "CREATE VIEW update_stored_view AS SELECT "+call+" AS sql")
		current := e.CatalogRead()
		table := current.MustRelation(enginetest.RoleTableRef(t, e, "update_stored_default").Address)
		definition, ok := table.ColumnMeta.Defaults[1]
		d, genuine := definition.Expression.(plan.InstalledDblinkBuildSQLUpdateExpr)
		if !ok || !genuine || definition.OID == 0 || d.Procedure != n.Procedure || d.Ref != n.Ref || !analyze.ClosedPgstattupleDefault(d, current) {
			t.Fatal("published DEFAULT lost genuine unevaluated recipe", definition)
		}
		text, err := (stored.ScalarRenderer{Owner: table, CapturedManifest: definition.Dependencies}).SQL(context.Background(), current, d)
		if err != nil || !strings.Contains(text, `"public"."dblink_build_sql_update"(`) {
			t.Fatal("published DEFAULT cannot render installed recipe", text, err)
		}
		relation := current.MustRelation(enginetest.CatalogRef(t, e, current, "update_stored_view").Address)
		owner := stored.ViewOf(relation)
		if owner == nil {
			t.Fatal("missing actual stored view owner")
		}
		project, ok := owner.Query().Root.(*plan.ProjectNode)
		if !ok || len(project.Expressions) != 1 || len(project.AnalysisExpressions) != 1 {
			t.Fatal("stored view lost executable/ancillary carrier")
		}
		for _, expression := range []expr.Expr{project.Expressions[0], project.AnalysisExpressions[0]} {
			v, ok := expression.(plan.InstalledDblinkBuildSQLUpdateExpr)
			if !ok || v.Procedure != n.Procedure || v.Ref != n.Ref || v.RoutineReference != n.Procedure || v.Properties().Volatility != expr.Volatile || !v.Properties().StageDependent {
				t.Fatal("stored view lost literal capture authority", expression)
			}
			if err := stored.ValidateInstalledDblinkBuildSQLUpdate(v, current); err != nil {
				t.Fatal(err)
			}
		}
		found := false
		for _, dependency := range owner.Dependencies() {
			found = found || dependency == (catalog.ExpressionDependency{Ref: n.Ref, Revision: n.Procedure.Revision(), Purpose: catalog.DefinitionDependency})
			if dependency.Ref.Address.ClassID == ids.RelationClassID {
				t.Fatal("stored TEXT lookup acquired relation dependency")
			}
		}
		if !found {
			t.Fatal("view omitted original installed procedure dependency")
		}
	})
}

func updateChildrenArray(children []expr.Expr) [5]expr.Expr {
	var out [5]expr.Expr
	copy(out[:], children)
	return out
}
