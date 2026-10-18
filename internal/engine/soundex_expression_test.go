package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func soundexUnitExpression(t *testing.T, e *Engine, name string) installedSoundexFunctionExpression {
	t.Helper()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	args := []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Ann"}}}
	if name == "difference" {
		args = append(args, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Anne"}})
	}
	node, err := binder.bindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return node.(installedSoundexFunctionExpression)
}

func TestSoundexExpressionIdentity(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	ctx := &EvalContext{Context: context.Background()}
	for i, name := range []string{"soundex", "difference"} {
		t.Run(name, func(t *testing.T) {
			n := soundexUnitExpression(t, e, name)
			if n.ref.Address != ext.Members[8+i] || n.procedure != e.catalog.state.Get(n.ref.Address) || plannedVolatility(n) != expr.Immutable || !generationImmutable(n) {
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
			if err := n.validate(e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			refs, err := pgDependExpressionRefs(ctx.Context, e.CatalogRead(), n)
			if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{n.ref.Address}) {
				t.Fatal(refs, err)
			}
			var children []expr.Expr
			boundExpressionChildren(n, func(child expr.Expr) { children = append(children, child) })
			if !reflect.DeepEqual(children, n.Args) || !sameGroupExpression(n, n) {
				t.Fatal(children)
			}
			changed := n
			changed.Args = append([]expr.Expr(nil), n.Args...)
			changed.Args[0] = literalExpression{Info: expr.InfoOf(n.Args[0]), value: sqltype.String(n.Args[0].ResultUsage().Type, "Andrew")}
			if sameGroupExpression(n, changed) {
				t.Fatal("argument identity collapsed")
			}
			other := n
			other.ref.Address = ext.Members[0]
			if sameGroupExpression(n, other) {
				t.Fatal("procedure identity collapsed")
			}
			consumer := &catalog.Relation{Generations: map[int32]catalog.ColumnGeneration{1: {Kind: catalog.GenerationStored, Expression: n, Planned: n, Dependencies: []ids.ObjectRef{n.ref}}}}
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
					p.Implementation = &fuzzystrmatchProvider{control: extensionControl{Version: "1.2"}}
				},
				func(p *catalog.Procedure) { p.Implementation = stockPgcrypto },
			} {
				state := catalog.CloneState(e.catalog.state)
				bad := *n.procedure
				mutate(&bad)
				state.SetDescriptor(n.ref.Address, &bad)
				forged := n
				forged.procedure = &bad
				if err := forged.validate(e.catalog.read(state)); err == nil {
					t.Fatal("forged identity", bad)
				}
			}
			for _, foreign := range []bool{false, true} {
				stale := n
				if foreign {
					stale.ref = New().catalog.Domain.Ref(stale.ref.Epoch, stale.ref.Address)
				} else {
					stale.ref.Epoch++
				}
				if _, err := (scalarRenderer{}).sql(ctx.Context, e.CatalogRead(), stale); err == nil {
					t.Fatal("foreign/stale reference")
				}
			}
			retagged, _, err := retagBoundTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil {
				t.Fatal(err)
			}
			if err := retagged.(installedSoundexFunctionExpression).validate(e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			folded, _, err := simplifyConditional(ctx, n, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := folded.(literalExpression); !ok {
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
				descriptor := remapExtensionDescriptor(before.Get(address), mapping)
				next.SetDescriptor(destination, descriptor)
				next.BindName(catalog.DescriptorNameKey(descriptor), destination)
			}
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900020}
			consumer := &catalog.Relation{ObjectAddress: address, Defaults: map[int32]catalog.ColumnDefault{1: {Expression: call, Dependencies: []ids.ObjectRef{call.ref}}}, Generations: map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored, Expression: call, Planned: call, Dependencies: []ids.ObjectRef{call.ref}}}}
			next.SetDescriptor(address, consumer)
			next.SetEpoch(next.Epoch() + 1)
			if err := e.reownCatalogExpressions(next, mapping, false); err != nil {
				t.Fatal(err)
			}
			view := e.catalog.read(next)
			relocated := next.MustRelation(address)
			for _, node := range []expr.Expr{relocated.Defaults[1].Expression, relocated.Generations[2].Expression, relocated.Generations[2].Planned} {
				n := node.(installedSoundexFunctionExpression)
				if n.ref.Address != mapping[call.ref.Address] || n.ref.Epoch != next.Epoch() || n.procedure != next.Get(n.ref.Address) {
					t.Fatal("stale remapped authority", n.ref)
				}
				if err := n.validate(view); err != nil {
					t.Fatal(err)
				}
				value, err := n.Eval(&EvalContext{Context: context.Background()})
				if err != nil || value.Type() != n.ResultUsage().Type {
					t.Fatal(value, err)
				}
			}
			if call.ref.Epoch != before.Epoch() || call.procedure != before.Get(call.ref.Address) {
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
		rewritten, err := binder.rewriteGroup(n, []expr.Expr{n.Args[0]})
		if err != nil {
			t.Fatal(err)
		}
		grouped := rewritten.(installedSoundexFunctionExpression)
		if _, ok := grouped.Args[0].(groupExpression); !ok || !grouped.Properties().StageDependent {
			t.Fatal("grouped child/stage lost", grouped)
		}
		if _, ok := n.Args[0].(literalExpression); !ok {
			t.Fatal("group rewrite mutated source")
		}
		for _, property := range []expr.Properties{
			{Volatility: expr.Stable}, {Volatility: expr.Volatile}, {RowDependent: true},
			{ParameterDependent: true}, {StageDependent: true}, {DomainDependent: true},
			{RuntimeDomainConversion: true},
		} {
			modified := n
			modified.Args = append([]expr.Expr(nil), n.Args...)
			modified.Args[0] = parameterExpression{Info: expr.Info{Usage: n.Args[0].ResultUsage(), Property: property}, number: 1}
			modified.Property = property
			if plannedVolatility(modified) != property.Volatility {
				t.Fatal("child volatility lost", property)
			}
			if property.Volatility != expr.Immutable && generationImmutable(modified) {
				t.Fatal("nonimmutable child admitted", property)
			}
			retagged, _, err := retagBoundTypes(e.CatalogRead(), modified, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil {
				t.Fatal(err)
			}
			got := retagged.(installedSoundexFunctionExpression)
			if got.Properties() != property || got.Args[0].Properties() != property {
				t.Fatal("retag discarded dependence", property, got.Properties())
			}
			if err := got.validate(e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
		}
	}
}
