package core_test

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/core"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func amcheckUnitExpression(t *testing.T, e *core.Engine, parent bool, options ...bool) plan.InstalledAmcheckExpr {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	name := "bt_index_check"
	if parent {
		name = "bt_index_parent_check"
	}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "i"}}}}
	for _, value := range options {
		raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: value}})
	}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledAmcheckExpr)
}
func TestAmcheckExpressionIdentity(t *testing.T) {
	e, _, _ := amcheckUnitRoot(t, false)
	n := amcheckUnitExpression(t, e, true)
	if n.Usage.Type.ID() != sqltype.Void || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent || len(n.Children()) != 1 || n.CallRoutineReference() != n.Procedure {
		t.Fatal(n)
	}
	rebuilt, err := n.Rebuild(expr.InfoOf(n), n.Children())
	if err != nil {
		t.Fatal(err)
	}
	copy := rebuilt.(plan.InstalledAmcheckExpr)
	if copy.Ref != n.Ref || copy.Procedure != n.Procedure || !reflect.DeepEqual(copy.Slots, n.Slots) || !copy.Parent {
		t.Fatal("rebuild lost selected identity", copy)
	}
	if err := stored.ValidateInstalledAmcheck(copy, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*plan.InstalledAmcheckExpr){
		func(n *plan.InstalledAmcheckExpr) { n.Parent = false },
		func(n *plan.InstalledAmcheckExpr) { n.RoutineReference = nil },
		func(n *plan.InstalledAmcheckExpr) { n.Ref.Address.ObjectID++ },
		func(n *plan.InstalledAmcheckExpr) {
			n.Usage.Type = e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 25}).SQLType
		},
	} {
		bad := n
		mutate(&bad)
		if err := stored.ValidateInstalledAmcheck(bad, e.CatalogRead()); err == nil {
			t.Fatal("forged expression accepted", bad)
		}
	}
	text, err := (stored.Deparser{View: e.CatalogRead()}).SQL(context.Background(), n, stored.DeparseParent{})
	if err != nil || text == "" {
		t.Fatal("deparse", text, err)
	}
	// Existing replacement transport remints the actual selected overload and
	// keeps the original capture immutable; there is no spelling-only fallback.
	view := e.CatalogRead()
	ext := view.MustExtension(n.Procedure.Extension)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
	for slot, address := range ext.Members {
		mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900001 + slot)}
	}
	state := catalog.CloneState(view.State())
	for old := range mapping {
		state.UnbindName(catalog.DescriptorNameKey(view.Get(old)))
		state.RemoveDescriptor(old)
	}
	for old, destination := range mapping {
		d := extension.RemapDescriptor(view.Get(old), mapping)
		state.SetDescriptor(destination, d)
		state.BindName(catalog.DescriptorNameKey(d), destination)
	}
	state.SetEpoch(state.Epoch() + 1)
	remap := func(ref ids.ObjectRef) ids.ObjectRef {
		ref.Epoch = state.Epoch()
		if address, ok := mapping[ref.Address]; ok {
			ref.Address = address
		}
		return ref
	}
	mapper := stored.ViewQueryRemapper{Catalog: e.Catalog.Read(state), Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(mapping, nil, nil)}
	query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "ok", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
	remapped, err := mapper.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	mapped := remapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledAmcheckExpr)
	if mapped.Ref != remap(n.Ref) || mapped.Procedure.Address() != mapping[n.Procedure.Address()] || mapped.CallRoutineReference() != mapped.Procedure || n.Procedure != view.Get(n.Ref.Address) {
		t.Fatal("remap lost or mutated identity", mapped)
	}
	if err := stored.ValidateInstalledAmcheck(mapped, e.Catalog.Read(state)); err != nil {
		t.Fatal(err)
	}
}
