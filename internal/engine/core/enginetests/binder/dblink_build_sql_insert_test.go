package binder_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestDblinkInsertCarrierReconstruction(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION dblink")
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dblink"}))
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := enginetest.FunctionCall("dblink_build_sql_insert", enginetest.StringLiteral("absent_relation"), enginetest.TypedLiteral("int2vector", "1 2"), enginetest.TypedLiteral("integer", "2"), enginetest.StringLiteral("{a,b}"), enginetest.StringLiteral("{remote,keys}"))
	bound, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := bound.(plan.InstalledDblinkBuildSQLInsertExpr)
	if !ok {
		t.Fatalf("not an authenticated carrier: %T", bound)
	}
	if n.Procedure != view.Get(ext.Members[1]) || n.CallRoutineReference() != n.Procedure || n.Ref.Address != ext.Members[1] || n.Ref.Epoch != view.Epoch() || n.Ref.Domain() != view.Owner().Domain || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent {
		t.Fatal("lost retained authority", n)
	}
	if err := stored.ValidateInstalledDblinkBuildSQLInsert(n, view); err != nil {
		t.Fatal(err)
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
		fresh := copy.(plan.InstalledDblinkBuildSQLInsertExpr)
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
		mutate func(*plan.InstalledDblinkBuildSQLInsertExpr)
	}{
		{"missing_routine", func(n *plan.InstalledDblinkBuildSQLInsertExpr) { n.RoutineReference = nil }},
		{"zero_epoch", func(n *plan.InstalledDblinkBuildSQLInsertExpr) { n.Ref.Epoch = 0 }},
		{"future_epoch", func(n *plan.InstalledDblinkBuildSQLInsertExpr) { n.Ref.Epoch++ }},
		{"wrong_address", func(n *plan.InstalledDblinkBuildSQLInsertExpr) { n.Ref.Address.ObjectID++ }},
		{"wrong_argument", func(n *plan.InstalledDblinkBuildSQLInsertExpr) { n.Args[1] = n.Args[0] }},
		{"wrong_result", func(n *plan.InstalledDblinkBuildSQLInsertExpr) { n.Usage = n.Args[2].ResultUsage() }},
	} {
		bad := n
		tc.mutate(&bad)
		if stored.ValidateInstalledDblinkBuildSQLInsert(bad, view) == nil {
			t.Fatal("forged retained recipe accepted", tc.name)
		}
	}
	staleState := catalog.CloneState(view.State())
	staleState.SetEpoch(view.Epoch() + 1)
	if stored.ValidateInstalledDblinkBuildSQLInsert(n, e.Catalog.Read(staleState)) == nil {
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
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 950000}, n.Procedure.Address(): {ClassID: ids.ProcedureClassID, ObjectID: 950001}, ext.Members[0]: {ClassID: ids.ProcedureClassID, ObjectID: 950002}}
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
	mapped := mappedQuery.(*plan.ProjectNode).Expressions[0].(plan.InstalledDblinkBuildSQLInsertExpr)
	if mapped.Ref != remap(n.Ref) || mapped.Procedure.Address() != mapping[n.Procedure.Address()] || mapped.CallRoutineReference() != mapped.Procedure || n.Procedure != view.Get(n.Ref.Address) || n.Ref.Epoch != view.Epoch() || !reflect.DeepEqual(n.Args, insertChildrenArray(children)) {
		t.Fatal("remap lost destination authority or mutated source")
	}
	if err := stored.ValidateInstalledDblinkBuildSQLInsert(mapped, e.Catalog.Read(state)); err != nil {
		t.Fatal(err)
	}
	current := *mapped.Procedure
	state.UnbindName(catalog.DescriptorNameKey(&current))
	current.ObjectName = "insert_renamed"
	state.SetDescriptor(current.Address(), &current)
	state.BindName(catalog.DescriptorNameKey(&current), current.Address())
	text, err := (stored.Deparser{View: e.Catalog.Read(state)}).SQL(context.Background(), mapped, stored.DeparseParent{})
	if err != nil || !strings.Contains(text, "insert_renamed(") || strings.Contains(text, "dblink_build_sql_insert(") {
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
}

func insertChildrenArray(children []expr.Expr) [5]expr.Expr {
	var out [5]expr.Expr
	copy(out[:], children)
	return out
}
