package binder_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func pgstatindexUnitExpression(t *testing.T, e *core.Engine) plan.InstalledPgstattupleStatsExpr {
	return pgstattupleUnitExpression(t, e, "pgstatindex")
}
func pgstattupleUnitExpression(t *testing.T, e *core.Engine, name string) plan.InstalledPgstattupleStatsExpr {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	value := parser.Value{Kind: parser.StringValue, Text: "i"}
	if name == "pgstathashindex" || name == "pgstatginindex" {
		value = parser.Value{Kind: parser.NullValue}
	}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{parser.LiteralExpr{Value: value}}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledPgstattupleStatsExpr)
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
				b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
				node, handled, err := b.BindSQLCallable(raw)
				if err != nil || !handled {
					t.Fatalf("installed overload not admitted: handled=%v err=%v", handled, err)
				}
				call, ok := node.(plan.InstalledPgstattupleStatsExpr)
				if !ok {
					t.Fatalf("selected call has type %T", node)
				}
				if call.Input != tc.input || call.Procedure.Signature != tc.signature || call.CallRoutineReference() != call.Procedure {
					t.Fatal("selected overload identity lost", call)
				}
				if err := stored.ValidateInstalledPgstattupleStats(call, e.CatalogRead()); err != nil {
					t.Fatal(err)
				}
				for _, input := range []sqltype.ID{0, sqltype.Int4, sqltype.Text, sqltype.Regclass} {
					if input == tc.input {
						continue
					}
					bad := call
					bad.Input = input
					if err := stored.ValidateInstalledPgstattupleStats(bad, e.CatalogRead()); err == nil {
						t.Fatal("unauthorized input/signature pair accepted", input, bad.Procedure.Signature)
					}
				}
			})
		}
	}
}
func TestPgstatindexExpressionIdentity(t *testing.T) {
	pgstattupleExpressionIdentity(t, "pgstatindex")
}
func TestPgstathashindexExpressionIdentity(t *testing.T) {
	pgstattupleExpressionIdentity(t, "pgstathashindex")
}
func TestPgstatginindexExpression(t *testing.T) {
	pgstattupleExpressionIdentity(t, "pgstatginindex")
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pgstatginindex"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}}
	node, handled, err := b.BindSQLCallable(raw)
	if err != nil || !handled {
		t.Fatalf("GIN callable gateway: handled=%v err=%v", handled, err)
	}
	n, ok := node.(plan.InstalledPgstattupleStatsExpr)
	if !ok || n.Operation != functions.GinStats || n.Input != sqltype.Regclass || n.Procedure.Signature != "2205" {
		t.Fatal("GIN selected declaration", node)
	}
	fields := []struct {
		name string
		typ  sqltype.ID
	}{{"version", sqltype.Int4}, {"pending_pages", sqltype.Int4}, {"pending_tuples", sqltype.Int8}}
	for i, want := range fields {
		member := n.ResultUsage().Type.CompositeMember(i)
		if member.Name != want.name || member.Attribute != int32(i+1) || member.Usage.Type.ID() != want.typ {
			t.Fatal("GIN owned output attribute", i, member)
		}
	}
	if _, err := n.Rebuild(expr.InfoOf(n), nil); err == nil {
		t.Fatal("missing child accepted")
	}
	info := expr.InfoOf(n)
	info.RoutineReference = nil
	if _, err := n.Rebuild(info, n.Children()); err == nil {
		t.Fatal("missing procedure authority accepted")
	}
	// Retag against real destination-owned generations. Detached Builtins
	// descriptors cannot satisfy the installed provider's exact OUT usages.
	view := e.CatalogRead()
	typedState := catalog.CloneState(view.State())
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Int8, sqltype.Regclass} {
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}
		owner := *view.MustType(address)
		generation := *owner.SQLType
		owner.SQLType = &generation
		typedState.SetDescriptor(address, &owner)
	}
	if changed, err := typedef.ReownDomainTypes(typedState, nil, false, nil, nil); err != nil || !changed {
		t.Fatal("new generations did not rebuild dependent types", changed, err)
	}
	typedView := e.Catalog.Read(typedState)
	oldShape, oldChildType, oldArgs := n.ResultUsage().Type, n.Args[0].ResultUsage().Type, &n.Args[0]
	retagged, changed, err := stored.RetagTypes(typedView, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		current, _ := typedView.LookupType(d.ID())
		return current
	})
	if err != nil || !changed {
		t.Fatalf("GIN retag: changed=%v err=%v", changed, err)
	}
	next := retagged.(plan.InstalledPgstattupleStatsExpr)
	if next.Operation != n.Operation || next.Input != n.Input || next.Procedure != n.Procedure || next.Ref != n.Ref || next.CallRoutineReference() != n.Procedure || next.ResultUsage().Type == oldShape {
		t.Fatal("GIN retag lost authority or retained old shape", next)
	}
	for i, want := range fields {
		member := next.ResultUsage().Type.CompositeMember(i)
		current, ok := typedView.LookupType(want.typ)
		if !ok || member.Name != want.name || member.Attribute != int32(i+1) || member.Usage != current.Usage() || member.Usage.Type == oldShape.CompositeMember(i).Usage.Type {
			t.Fatal("GIN retag lost destination OUT authority", i, member)
		}
	}
	currentInput, _ := typedView.LookupType(sqltype.Regclass)
	literal, ok := next.Args[0].(plan.LiteralExpr)
	if !ok || next.Args[0].ResultUsage().Type != currentInput || literal.Value.Type() != currentInput || &next.Args[0] == oldArgs {
		t.Fatal("GIN retag lost destination operand authority or shared source backing")
	}
	if err := stored.ValidateInstalledPgstattupleStats(next, typedView); err != nil {
		t.Fatal("GIN retag lost installed provider authority", err)
	}
	if n.ResultUsage().Type != oldShape || n.Args[0].ResultUsage().Type != oldChildType || &n.Args[0] != oldArgs {
		t.Fatal("GIN retag mutated original artifact")
	}
	if _, err := n.Rebuild(expr.InfoOf(n), []expr.Expr{n.Args[0], n.Args[0]}); err == nil {
		t.Fatal("extra child accepted")
	}
	boolean := e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 16}).SQLType
	childInfo := expr.InfoOf(n.Args[0])
	childInfo.Usage = boolean.Usage()
	wrongChild := plan.LiteralExpr{Info: childInfo, Value: sqltype.Null(boolean)}
	if _, err := n.Rebuild(expr.InfoOf(n), []expr.Expr{wrongChild}); err == nil {
		t.Fatal("boolean child accepted for regclass-only declaration")
	}
	badOperation := n
	badOperation.Operation = functions.HashStats
	if err := stored.ValidateInstalledPgstattupleStats(badOperation, e.CatalogRead()); err == nil {
		t.Fatal("GIN authority admitted hash operation")
	}
	badInput := n
	badInput.Input = sqltype.Text
	if err := badInput.ValidateArguments(n.Children()); err == nil {
		t.Fatal("GIN operation admitted text input")
	}
}
func pgstattupleExpressionIdentity(t *testing.T, name string) {
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	n := pgstattupleUnitExpression(t, e, name)
	if n.Usage.Type.ID() != 2249 || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent || len(n.Children()) != 1 || n.CallRoutineReference() != n.Procedure {
		t.Fatal(n)
	}
	wantFields := 10
	if name == "pgstathashindex" {
		wantFields = 8
	}
	if name == "pgstatginindex" {
		wantFields = 3
	}
	if n.ResultUsage().Type.CompositeLen() != wantFields {
		t.Fatal("owned shape", n.ResultUsage())
	}
	rebuilt, err := n.Rebuild(expr.InfoOf(n), n.Children())
	if err != nil {
		t.Fatal(err)
	}
	copy := rebuilt.(plan.InstalledPgstattupleStatsExpr)
	if copy.Ref != n.Ref || copy.Procedure != n.Procedure || copy.Input != n.Input || copy.Operation != n.Operation || copy.ResultUsage().Type != n.ResultUsage().Type {
		t.Fatal("rebuild lost selected identity", copy)
	}
	if err := stored.ValidateInstalledPgstattupleStats(copy, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*plan.InstalledPgstattupleStatsExpr){
		func(n *plan.InstalledPgstattupleStatsExpr) {
			if n.Input == sqltype.Text {
				n.Input = sqltype.Regclass
			} else {
				n.Input = sqltype.Text
			}
		},
		func(n *plan.InstalledPgstattupleStatsExpr) { n.RoutineReference = nil },
		func(n *plan.InstalledPgstattupleStatsExpr) { n.Ref.Address.ObjectID++ },
		func(n *plan.InstalledPgstattupleStatsExpr) { n.Operation ^= 1 },
		func(n *plan.InstalledPgstattupleStatsExpr) {
			n.Usage.Type = e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 25}).SQLType
		},
	} {
		bad := n
		mutate(&bad)
		if err := stored.ValidateInstalledPgstattupleStats(bad, e.CatalogRead()); err == nil {
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
	mapped := remapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgstattupleStatsExpr)
	if mapped.Ref != remap(n.Ref) || mapped.Procedure.Address() != mapping[n.Procedure.Address()] || mapped.CallRoutineReference() != mapped.Procedure || mapped.Operation != n.Operation || mapped.Input != n.Input || mapped.ResultUsage().Type.CompositeLen() != n.ResultUsage().Type.CompositeLen() || n.Procedure != view.Get(n.Ref.Address) {
		t.Fatal("remap lost or mutated identity", mapped)
	}
	if err := stored.ValidateInstalledPgstattupleStats(mapped, e.Catalog.Read(state)); err != nil {
		t.Fatal(err)
	}
}
