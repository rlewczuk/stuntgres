package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func soundexUnitExpression(t *testing.T, e *Engine, name string) plan.InstalledSoundexExpr {
	t.Helper()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	args := []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Ann"}}}
	if name == "difference" {
		args = append(args, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Anne"}})
	}
	node, err := binder.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledSoundexExpr)
}

func TestSoundexExpressionIdentity(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	for i, name := range []string{"soundex", "difference"} {
		t.Run(name, func(t *testing.T) {
			n := soundexUnitExpression(t, e, name)
			if n.Ref.Address != ext.Members[8+i] || n.Procedure != e.catalog.state.Get(n.Ref.Address) || exec.PlannedVolatility(n) != expr.Immutable || !plan.GenerationImmutable(n) {
				t.Fatal("identity/properties", n)
			}
			value, err := n.Eval(ctx)
			if err != nil || value.Type() != n.ResultUsage().Type {
				t.Fatal(value, err)
			}
			if name == "soundex" {
				if code, err := value.Text(); err != nil || code != "A500" {
					t.Fatal(code, err)
				}
			} else if score, err := value.Integer(); err != nil || score != 4 {
				t.Fatal(score, err)
			}
			if err := validateInstalledSoundex(n, e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			refs, err := pgDependExpressionRefs(ctx.Context, e.CatalogRead(), n)
			if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{n.Ref.Address}) {
				t.Fatal(refs, err)
			}
			var children []expr.Expr
			plan.ExpressionChildren(n, func(child expr.Expr) { children = append(children, child) })
			if !reflect.DeepEqual(children, n.Args) || !analyze.SameGroupExpression(n, n) {
				t.Fatal(children)
			}
			changed := n
			changed.Args = append([]expr.Expr(nil), n.Args...)
			changed.Args[0] = plan.LiteralExpr{Info: expr.InfoOf(n.Args[0]), Value: sqltype.String(n.Args[0].ResultUsage().Type, "Andrew")}
			if analyze.SameGroupExpression(n, changed) {
				t.Fatal("argument identity collapsed")
			}
			other := n
			other.Ref.Address = ext.Members[0]
			if analyze.SameGroupExpression(n, other) {
				t.Fatal("procedure identity collapsed")
			}
			consumer := &catalog.Relation{ColumnMeta: catalog.ColumnMetadata{Generations: map[int32]catalog.ColumnGeneration{1: {Kind: catalog.GenerationStored, Expression: n, Planned: n, Dependencies: []ids.ObjectRef{n.Ref}}}}}
			if err := validateGenerationProviders(e.CatalogRead(), consumer); err != nil {
				t.Fatal(err)
			}
			if err := validateGenerationPlan(e.CatalogRead(), consumer, n); err != nil {
				t.Fatal(err)
			}
			for _, mutate := range []func(*catalog.Procedure){
				func(p *catalog.Procedure) { p.Result = 1009 },
				func(p *catalog.Procedure) { p.Signature = "23" },
				func(p *catalog.Procedure) { p.ObjectName = "text_soundex" },
				func(p *catalog.Procedure) {
					p.Implementation = new(*extension.StockFuzzystrmatch)
				},
				func(p *catalog.Procedure) { p.Implementation = extension.StockPgcrypto },
			} {
				state := catalog.CloneState(e.catalog.state)
				bad := *n.Procedure
				mutate(&bad)
				state.SetDescriptor(n.Ref.Address, &bad)
				forged := n
				forged.Procedure = &bad
				if err := validateInstalledSoundex(forged, e.catalog.read(state)); err == nil {
					t.Fatal("forged identity", bad)
				}
			}
			for _, foreign := range []bool{false, true} {
				stale := n
				if foreign {
					stale.Ref = New().catalog.Domain.Ref(stale.Ref.Epoch, stale.Ref.Address)
				} else {
					stale.Ref.Epoch++
				}
				if _, err := (scalarRenderer{}).sql(ctx.Context, e.CatalogRead(), stale); err == nil {
					t.Fatal("foreign/stale reference")
				}
			}
			retagged, _, err := retagBoundTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil {
				t.Fatal(err)
			}
			if err := validateInstalledSoundex(retagged.(plan.InstalledSoundexExpr), e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			folded, _, err := simplifyConditional(ctx, n, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := folded.(plan.LiteralExpr); !ok {
				t.Fatal("immutable call not folded", folded)
			}
		})
	}
}

func TestSoundexExpressionRemap(t *testing.T) {
	for _, name := range []string{"soundex", "difference"} {
		t.Run(name, func(t *testing.T) {
			e, ext := daitchUnitInstalled(t)
			call := soundexUnitExpression(t, e, name)
			before := e.CatalogRead()
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
			for i, address := range ext.Members {
				mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900001 + i)}
			}
			next := catalog.CloneState(before.State())
			for address := range mapping {
				next.UnbindName(catalog.DescriptorNameKey(before.Get(address)))
				next.RemoveDescriptor(address)
			}
			for address, destination := range mapping {
				descriptor := extension.RemapDescriptor(before.Get(address), mapping)
				next.SetDescriptor(destination, descriptor)
				next.BindName(catalog.DescriptorNameKey(descriptor), destination)
			}
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900020}
			consumer := &catalog.Relation{ObjectAddress: address, ColumnMeta: catalog.ColumnMetadata{Defaults: map[int32]catalog.ColumnDefault{1: {Expression: call, Dependencies: []ids.ObjectRef{call.Ref}}}, Generations: map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored, Expression: call, Planned: call, Dependencies: []ids.ObjectRef{call.Ref}}}}}
			next.SetDescriptor(address, consumer)
			next.SetEpoch(next.Epoch() + 1)
			if err := e.reownCatalogExpressions(next, mapping, false); err != nil {
				t.Fatal(err)
			}
			view := e.catalog.read(next)
			relocated := next.MustRelation(address)
			for _, node := range []expr.Expr{relocated.ColumnMeta.Defaults[1].Expression, relocated.ColumnMeta.Generations[2].Expression, relocated.ColumnMeta.Generations[2].Planned} {
				n := node.(plan.InstalledSoundexExpr)
				if n.Ref.Address != mapping[call.Ref.Address] || n.Ref.Epoch != next.Epoch() || n.Procedure != next.Get(n.Ref.Address) {
					t.Fatal("stale remapped authority", n.Ref)
				}
				if err := validateInstalledSoundex(n, view); err != nil {
					t.Fatal(err)
				}
				value, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
				if err != nil || value.Type() != n.ResultUsage().Type {
					t.Fatal(value, err)
				}
			}
			if call.Ref.Epoch != before.Epoch() || call.Procedure != before.Get(call.Ref.Address) {
				t.Fatal("source mutated")
			}
		})
	}
}

func TestSoundexExpressionProperties(t *testing.T) {
	e, _ := daitchUnitInstalled(t)
	for _, name := range []string{"soundex", "difference"} {
		n := soundexUnitExpression(t, e, name)
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		rewritten, err := binder.RewriteGroup(n, []expr.Expr{n.Args[0]})
		if err != nil {
			t.Fatal(err)
		}
		grouped := rewritten.(plan.InstalledSoundexExpr)
		if _, ok := grouped.Args[0].(plan.GroupExpr); !ok || !grouped.Properties().StageDependent {
			t.Fatal("grouped child/stage lost", grouped)
		}
		if _, ok := n.Args[0].(plan.LiteralExpr); !ok {
			t.Fatal("group rewrite mutated source")
		}
		for _, property := range []expr.Properties{
			{Volatility: expr.Stable}, {Volatility: expr.Volatile}, {RowDependent: true},
			{ParameterDependent: true}, {StageDependent: true}, {DomainDependent: true},
			{RuntimeDomainConversion: true},
		} {
			modified := n
			modified.Args = append([]expr.Expr(nil), n.Args...)
			modified.Args[0] = plan.ParameterExpr{Info: expr.Info{Usage: n.Args[0].ResultUsage(), Property: property}, Number: 1}
			modified.Property = property
			if exec.PlannedVolatility(modified) != property.Volatility {
				t.Fatal("child volatility lost", property)
			}
			if property.Volatility != expr.Immutable && plan.GenerationImmutable(modified) {
				t.Fatal("nonimmutable child admitted", property)
			}
			retagged, _, err := retagBoundTypes(e.CatalogRead(), modified, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil {
				t.Fatal(err)
			}
			got := retagged.(plan.InstalledSoundexExpr)
			if got.Properties() != property || got.Args[0].Properties() != property {
				t.Fatal("retag discarded dependence", property, got.Properties())
			}
			if err := validateInstalledSoundex(got, e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
		}
	}
}
