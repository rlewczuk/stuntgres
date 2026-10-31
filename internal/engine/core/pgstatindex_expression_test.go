package core_test

import (
	"context"
	"stuntgres/internal/engine"
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

func pgstatindexUnitExpression(t *testing.T, e *core.Engine) plan.InstalledPgstatindexExpr {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pgstatindex"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "i"}}}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledPgstatindexExpr)
}

// Exercise the public-call gateway, not BindFunctionProvider's already-selected
// family path: skipping catalog admission silently chooses the text declaration.
func TestPgstatindexCallableResolution(t *testing.T) {
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	null := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	for _, tc := range []struct {
		name      string
		cast      string
		input     sqltype.ID
		signature catalog.ProcedureSignature
	}{
		{name: "unknown", input: sqltype.Text, signature: "25"},
		{name: "text", cast: "text", input: sqltype.Text, signature: "25"},
		{name: "regclass", cast: "regclass", input: sqltype.Regclass, signature: "2205"},
		{name: "oid", cast: "oid", input: sqltype.Regclass, signature: "2205"},
		{name: "int4", cast: "int4", input: sqltype.Regclass, signature: "2205"},
	} {
		for _, named := range []bool{false, true} {
			label := "positional"
			if named {
				label = "named"
			}
			t.Run(tc.name+"/"+label, func(t *testing.T) {
				var arg parser.Expr = null
				if tc.cast != "" {
					arg = parser.CastExpr{Child: null, TypeName: parser.TypeName{Name: tc.cast}, Syntax: parser.PostfixCast}
				}
				raw := parser.FunctionExpr{
					Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pgstatindex"}}},
					Args: []parser.Expr{arg},
				}
				if named {
					raw.ArgNames = []parser.Identifier{{Text: "relname"}}
				}
				b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
				node, handled, err := b.BindSQLCallable(raw)
				if err != nil || !handled {
					t.Fatalf("installed overload not admitted: handled=%v err=%v", handled, err)
				}
				call, ok := node.(plan.InstalledPgstatindexExpr)
				if !ok {
					t.Fatalf("selected call has type %T", node)
				}
				if call.Input != tc.input || call.Procedure.Signature != tc.signature || call.CallRoutineReference() != call.Procedure {
					t.Fatal("selected overload identity lost", call)
				}
				if err := stored.ValidateInstalledPgstatindex(call, e.CatalogRead()); err != nil {
					t.Fatal(err)
				}
				for _, input := range []sqltype.ID{0, sqltype.Int4, sqltype.Text, sqltype.Regclass} {
					if input == tc.input {
						continue
					}
					bad := call
					bad.Input = input
					if err := stored.ValidateInstalledPgstatindex(bad, e.CatalogRead()); err == nil {
						t.Fatal("unauthorized input/signature pair accepted", input, bad.Procedure.Signature)
					}
				}
			})
		}
	}
}
func TestPgstatindexExpressionIdentity(t *testing.T) {
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	n := pgstatindexUnitExpression(t, e)
	if n.Usage.Type.ID() != 2249 || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent || len(n.Children()) != 1 || n.CallRoutineReference() != n.Procedure {
		t.Fatal(n)
	}
	rebuilt, err := n.Rebuild(expr.InfoOf(n), n.Children())
	if err != nil {
		t.Fatal(err)
	}
	copy := rebuilt.(plan.InstalledPgstatindexExpr)
	if copy.Ref != n.Ref || copy.Procedure != n.Procedure || copy.Input != n.Input {
		t.Fatal("rebuild lost selected identity", copy)
	}
	if err := stored.ValidateInstalledPgstatindex(copy, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*plan.InstalledPgstatindexExpr){
		func(n *plan.InstalledPgstatindexExpr) { n.Input = sqltype.Regclass },
		func(n *plan.InstalledPgstatindexExpr) { n.RoutineReference = nil },
		func(n *plan.InstalledPgstatindexExpr) { n.Ref.Address.ObjectID++ },
		func(n *plan.InstalledPgstatindexExpr) {
			n.Usage.Type = e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 25}).SQLType
		},
	} {
		bad := n
		mutate(&bad)
		if err := stored.ValidateInstalledPgstatindex(bad, e.CatalogRead()); err == nil {
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
	mapped := remapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgstatindexExpr)
	if mapped.Ref != remap(n.Ref) || mapped.Procedure.Address() != mapping[n.Procedure.Address()] || mapped.CallRoutineReference() != mapped.Procedure || n.Procedure != view.Get(n.Ref.Address) {
		t.Fatal("remap lost or mutated identity", mapped)
	}
	if err := stored.ValidateInstalledPgstatindex(mapped, e.Catalog.Read(state)); err != nil {
		t.Fatal(err)
	}
}
