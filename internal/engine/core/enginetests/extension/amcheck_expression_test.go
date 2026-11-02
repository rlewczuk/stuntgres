package extension_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func amcheckUnitExpression(t *testing.T, e *core.Engine, parent bool, options ...bool) plan.InstalledAmcheckExpr {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
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
	testAmcheckExpressionIdentity(t, false)
}

func TestAmcheckGINExpressionIdentity(t *testing.T) {
	testAmcheckExpressionIdentity(t, true)
}

func testAmcheckExpressionIdentity(t *testing.T, gin bool) {
	e, _, _ := amcheckUnitRoot(t, false)
	n := amcheckUnitExpression(t, e, true)
	if gin {
		enginetest.Run(t, e, "CREATE TABLE gin_keys(a integer[])")
		enginetest.Run(t, e, "CREATE INDEX gin_i ON gin_keys USING gin(a)")
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "gin_index_check"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "gin_i"}}}}
		node, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		n = node.(plan.InstalledAmcheckExpr)
	}
	if n.Usage.Type.ID() != sqltype.Void || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent || len(n.Children()) != 1 || n.CallRoutineReference() != n.Procedure {
		t.Fatal(n)
	}
	rebuilt, err := n.Rebuild(expr.InfoOf(n), n.Children())
	if err != nil {
		t.Fatal(err)
	}
	copy := rebuilt.(plan.InstalledAmcheckExpr)
	if copy.Ref != n.Ref || copy.Procedure != n.Procedure || !reflect.DeepEqual(copy.Slots, n.Slots) || copy.Kind != n.Kind || gin && copy.Kind != functions.AmcheckGIN || !gin && copy.Kind != functions.AmcheckBtreeParent {
		t.Fatal("rebuild lost selected identity", copy)
	}
	if !expr.AdmitsSource(n) || !expr.AdmitsSource(copy) || expr.IsSetSource(n) || expr.IsSetSource(copy) || expr.AdmitsSource(n.Amcheck) {
		t.Fatal("scalar source admission lost installed wrapper authority")
	}
	if err := stored.ValidateInstalledAmcheck(copy, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	substituted, err := n.Substitute(expr.InfoOf(n), n.Children())
	if err != nil || substituted.(plan.InstalledAmcheckExpr).Kind != n.Kind {
		t.Fatal("substitution lost checker kind", substituted, err)
	}
	captured, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || captured.Count != 1 || &captured.Args[0] != &n.Args[0] {
		t.Fatal("capture lost written operands", captured, err)
	}
	retagged, _, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil || retagged.(plan.InstalledAmcheckExpr).Kind != n.Kind || retagged.(plan.InstalledAmcheckExpr).Procedure != n.Procedure {
		t.Fatal("retag lost checker identity", retagged, err)
	}
	for _, mutate := range []func(*plan.InstalledAmcheckExpr){
		func(n *plan.InstalledAmcheckExpr) { n.Kind = functions.AmcheckBtree },
		func(n *plan.InstalledAmcheckExpr) { n.Kind = functions.AmcheckKind(255) },
		func(n *plan.InstalledAmcheckExpr) { n.RoutineReference = nil },
		func(n *plan.InstalledAmcheckExpr) { n.Ref.Address.ObjectID++ },
		func(n *plan.InstalledAmcheckExpr) { n.Ref = new(ids.Domain).Ref(n.Ref.Epoch, n.Ref.Address) },
		func(n *plan.InstalledAmcheckExpr) { n.Slots[0] = 1 },
		func(n *plan.InstalledAmcheckExpr) { n.Args = nil },
		func(n *plan.InstalledAmcheckExpr) {
			boolean := e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 16}).SQLType
			n.Args = []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Null(boolean)}}
		},
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
	if err != nil || text == "" || !strings.Contains(text, "index") || !strings.Contains(text, "=>") || gin && !strings.Contains(text, "gin_index_check") {
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
		if address, ok := mapping[ref.Address]; ok {
			ref.Address = address
		}
		return ref
	}
	mapper := stored.ViewQueryRemapper{Catalog: e.Catalog.Read(state), Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(descriptor, nil, nil)}
	query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "ok", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
	remapped, err := mapper.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	mapped := remapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledAmcheckExpr)
	if mapped.Ref != remap(n.Ref) || mapped.Kind != n.Kind || mapped.Slots != n.Slots || mapped.Procedure.Address() != mapping[n.Procedure.Address()] || mapped.CallRoutineReference() != mapped.Procedure || n.Procedure != view.Get(n.Ref.Address) {
		t.Fatal("remap lost or mutated identity", mapped)
	}
	if err := stored.ValidateInstalledAmcheck(mapped, e.Catalog.Read(state)); err != nil {
		t.Fatal(err)
	}
}

func amcheckUnitRoot(t *testing.T, unique bool) (*core.Engine, *catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	e := engine.New()
	amcheckUnitInstalled(t, e)
	enginetest.Run(t, e, "CREATE TABLE t(n INT,s TEXT)")
	if unique {
		enginetest.Run(t, e, "INSERT INTO t VALUES(1,'a'),(2,'b'),(NULL,NULL)")
	} else {
		enginetest.Run(t, e, "INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')")
	}
	prefix := "CREATE INDEX"
	if unique {
		prefix = "CREATE UNIQUE INDEX"
	}
	enginetest.Run(t, e, prefix+" i ON t(n)")
	d, root := btreeRoot(t, e, "i")
	return e, d, root
}

func btreeRoot(t *testing.T, e *core.Engine, name string) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	ref := enginetest.RoleTableRef(t, e, name)
	idx := e.Catalog.State.MustRelation(ref.Address)
	root := e.Storage.IndexRoots[ref.Address]
	if idx.RelationKind != catalog.IndexRelation || idx.SQLIndex == nil || root == nil || e.Storage.Relations[ref.Address] != nil {
		t.Fatal("not a substantive index", idx, root)
	}
	if err := index.ValidateIndexDefinition(e.CatalogRead(), idx); err != nil {
		t.Fatal(err)
	}
	return idx, root
}
