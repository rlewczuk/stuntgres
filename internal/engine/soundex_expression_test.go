package engine

import (
	"context"
	"reflect"
	"testing"

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
	node, err := binder.bindInstalledSoundex(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args})
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
			if n.ref.Address != ext.members[8+i] || n.procedure != e.state.descriptors[n.ref.Address] || plannedVolatility(n) != Immutable || !generationImmutable(n) {
				t.Fatal("identity/properties", n)
			}
			value, err := n.Eval(ctx)
			if err != nil || value.Type() != n.ResultUsage().Type {
				t.Fatal(value, err)
			}
			if name == "soundex" {
				if code, err := value.Text(); err != nil || code != "A500" { t.Fatal(code, err) }
			} else if score, err := value.Integer(); err != nil || score != 4 { t.Fatal(score, err) }
			if err := n.validate(e.CatalogRead()); err != nil { t.Fatal(err) }
			refs, err := pgDependExpressionRefs(ctx.Context, e.CatalogRead(), n)
			if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{n.ref.Address}) { t.Fatal(refs, err) }
			var children []BoundExpr
			boundExpressionChildren(n, func(child BoundExpr) { children = append(children, child) })
			if !reflect.DeepEqual(children, n.Args) || !sameGroupExpression(n, n) { t.Fatal(children) }
			changed := n
			changed.Args = append([]BoundExpr(nil), n.Args...)
			changed.Args[0] = literalExpression{expressionInfo: boundExpressionInfo(n.Args[0]), value: sqltype.String(n.Args[0].ResultUsage().Type, "Andrew")}
			if sameGroupExpression(n, changed) { t.Fatal("argument identity collapsed") }
			other := n
			other.ref.Address = ext.members[0]
			if sameGroupExpression(n, other) { t.Fatal("procedure identity collapsed") }
			consumer := &relationDescriptor{generations: map[int32]columnGeneration{1: {kind: generationStored, expression: n, planned: n, dependencies: []ids.ObjectRef{n.ref}}}}
			if err := validateGenerationProviders(e.CatalogRead(), consumer); err != nil { t.Fatal(err) }
			if err := validateGenerationPlan(e.CatalogRead(), consumer, n); err != nil { t.Fatal(err) }
			for _, mutate := range []func(*procedureDescriptor){
				func(p *procedureDescriptor) { p.result = 1009 },
				func(p *procedureDescriptor) { p.signature = "23" },
				func(p *procedureDescriptor) { p.name = "text_soundex" },
				func(p *procedureDescriptor) { p.fuzzy = &fuzzystrmatchProvider{version: "1.2"} },
				func(p *procedureDescriptor) { p.crypto = stockPgcrypto },
			} {
				state := cloneCatalog(e.state)
				bad := *n.procedure
				mutate(&bad)
				state.descriptors[n.ref.Address] = &bad
				forged := n
				forged.procedure = &bad
				if err := forged.validate(CatalogRead{owner: e, state: state}); err == nil { t.Fatal("forged identity", bad) }
			}
			for _, foreign := range []bool{false, true} {
				stale := n
				if foreign { stale.ref = New().domain.Ref(stale.ref.Epoch, stale.ref.Address) } else { stale.ref.Epoch++ }
				if _, err := (scalarRenderer{}).sql(ctx.Context, e.CatalogRead(), stale); err == nil { t.Fatal("foreign/stale reference") }
			}
			retagged, _, err := retagBoundTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil { t.Fatal(err) }
			if err := retagged.(installedSoundexFunctionExpression).validate(e.CatalogRead()); err != nil { t.Fatal(err) }
			folded, _, err := simplifyConditional(ctx, n, false)
			if err != nil { t.Fatal(err) }
			if _, ok := folded.(literalExpression); !ok { t.Fatal("immutable call not folded", folded) }
		})
	}
}

func TestSoundexExpressionRemap(t *testing.T) {
	for _, name := range []string{"soundex", "difference"} {
		t.Run(name, func(t *testing.T) {
			e, ext := daitchUnitInstalled(t)
			call := soundexUnitExpression(t, e, name)
			before := e.CatalogRead()
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.address: {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
			for i, address := range ext.members { mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900001+i)} }
			next := cloneCatalog(before.state)
			for address := range mapping {
				delete(next.names, descriptorNameKey(before.state.descriptors[address]))
				delete(next.descriptors, address)
			}
			for address, destination := range mapping {
				descriptor := remapExtensionDescriptor(before.state.descriptors[address], mapping)
				next.descriptors[destination] = descriptor
				next.names[descriptorNameKey(descriptor)] = destination
			}
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900020}
			consumer := &relationDescriptor{address: address, defaults: map[int32]columnDefault{1: {expression: call, dependencies: []ids.ObjectRef{call.ref}}}, generations: map[int32]columnGeneration{2: {kind: generationStored, expression: call, planned: call, dependencies: []ids.ObjectRef{call.ref}}}}
			next.descriptors[address] = consumer
			next.epoch++
			if err := e.reownCatalogExpressions(next, mapping, false); err != nil { t.Fatal(err) }
			view := CatalogRead{owner: e, state: next}
			relocated := next.descriptors[address].(*relationDescriptor)
			for _, node := range []BoundExpr{relocated.defaults[1].expression, relocated.generations[2].expression, relocated.generations[2].planned} {
				n := node.(installedSoundexFunctionExpression)
				if n.ref.Address != mapping[call.ref.Address] || n.ref.Epoch != next.epoch || n.procedure != next.descriptors[n.ref.Address] { t.Fatal("stale remapped authority", n.ref) }
				if err := n.validate(view); err != nil { t.Fatal(err) }
				value, err := n.Eval(&EvalContext{Context: context.Background()})
				if err != nil || value.Type() != n.ResultUsage().Type { t.Fatal(value, err) }
			}
			if call.ref.Epoch != before.Epoch() || call.procedure != before.state.descriptors[call.ref.Address] { t.Fatal("source mutated") }
		})
	}
}

func TestSoundexExpressionProperties(t *testing.T) {
	e, _ := daitchUnitInstalled(t)
	for _, name := range []string{"soundex", "difference"} {
		n := soundexUnitExpression(t, e, name)
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		rewritten, err := binder.rewriteGroup(n, []BoundExpr{n.Args[0]})
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
		for _, property := range []ExprProperties{
			{Volatility: Stable}, {Volatility: Volatile}, {RowDependent: true},
			{ParameterDependent: true}, {StageDependent: true}, {DomainDependent: true},
			{RuntimeDomainConversion: true},
		} {
			modified := n
			modified.Args = append([]BoundExpr(nil), n.Args...)
			modified.Args[0] = parameterExpression{expressionInfo: expressionInfo{Usage: n.Args[0].ResultUsage(), Property: property}, number: 1}
			modified.Property = property
			if plannedVolatility(modified) != property.Volatility {
				t.Fatal("child volatility lost", property)
			}
			if property.Volatility != Immutable && generationImmutable(modified) {
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
