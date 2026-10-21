package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func intaggUnitRecipes(t *testing.T) {
	for _, call := range []string{"int_array_aggregate(2)", "int_array_enum('{2,2,NULL}')"} {
		t.Run(call, func(t *testing.T) {
			e, ext := intaggUnitInstalled(t)
			ctx := context.Background()
			b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			b.Clause.AggregateClause = "SELECT"
			syntax := statement(t, "SELECT "+call).(*parser.Select)
			raw := syntax.Targets[0]
			node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
				t.Fatal(err)
			}
			path := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral}
			name := extension.IntaggRecipeFunction(raw)
			ref, ok := b.Capture.RoutineRefs[path]
			if !ok || ref.Address == (ids.ObjectAddress{}) || len(b.Capture.RoutineRefs) != 1 {
				t.Fatal("missing bound occurrence", b.Capture.RoutineRefs)
			}
			var child plan.Node = &plan.SingletonNode{}
			if len(b.Grouping.Aggregates) != 0 {
				child = &plan.AggregateNode{Child: child, Calls: b.Grouping.Aggregates}
			}
			project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: node.ResultUsage()}}, Child: child, Expressions: []expr.Expr{node}, Frame: b.FrameID(), Syntax: syntax}
			definition := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: project}, Syntax: syntax, Dependencies: b.Dependencies, RoutineRefs: b.Capture.RoutineRefs, Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages})
			definition = definition.With(func(p *stored.ViewParts) { p.Frames, p.Queries = analyze.CollectQueryFrames(project) })
			if err := definition.ValidateLiterals(e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			renderer := stored.NewQueryRenderer(definition)
			renderer.Frame = b.FrameID()
			text, err := renderer.ExpressionSQL(ctx, e.CatalogRead(), raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
			if err != nil || !strings.HasPrefix(text, `"public"."`+name+`"(`) {
				t.Fatal("unqualified bound recipe", text, err)
			}
			for _, badRef := range []ids.ObjectRef{{}, New().catalog.Domain.Ref(ref.Epoch, ref.Address), ref.Domain().Ref(ref.Epoch+1, ref.Address), ref.Domain().Ref(ref.Epoch, ext.Address())} {
				bad := definition.With(func(p *stored.ViewParts) { p.RoutineRefs = map[analyze.ViewLiteralPath]ids.ObjectRef{path: badRef} })
				if err := bad.ValidateLiterals(e.CatalogRead()); err == nil {
					t.Fatal("forged frozen reference accepted", badRef)
				}
			}
			missing := definition.With(func(p *stored.ViewParts) { p.RoutineRefs = nil })
			if err := missing.ValidateLiterals(e.CatalogRead()); err == nil {
				t.Fatal("raw-name fallback accepted")
			}
			extra := definition.With(func(p *stored.ViewParts) {
				p.RoutineRefs = map[analyze.ViewLiteralPath]ids.ObjectRef{path: ref, {Query: b.FrameID(), Target: 99}: ref}
			})
			if err := extra.ValidateLiterals(e.CatalogRead()); err == nil {
				t.Fatal("unused frozen capture accepted")
			}
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 910000}}
			for i, address := range ext.Members {
				mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(910001 + i)}
			}
			state := catalog.CloneState(e.catalog.state)
			for address := range mapping {
				state.UnbindName(catalog.DescriptorNameKey(e.catalog.state.Get(address)))
				state.RemoveDescriptor(address)
			}
			for address, destination := range mapping {
				d := extension.RemapDescriptor(e.catalog.state.Get(address), mapping)
				state.SetDescriptor(destination, d)
				state.BindName(catalog.DescriptorNameKey(d), destination)
			}
			state.SetEpoch(state.Epoch() + 1)
			view := e.catalog.read(state)
			mapRef := func(old ids.ObjectRef) ids.ObjectRef {
				old.Epoch = state.Epoch()
				if destination, ok := mapping[old.Address]; ok {
					old.Address = destination
				}
				return old
			}
			mapped, err := stored.RemapView(view, definition, mapping, mapRef, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil)
			if err != nil {
				t.Fatal(err)
			}
			if mapped.RoutineRefs()[path] != mapRef(ref) || definition.RoutineRefs()[path] != ref {
				t.Fatal("capture remap mutated/lost authority")
			}
			if err := mapped.ValidateLiterals(view); err != nil {
				t.Fatal(err)
			}
			proc, err := mapped.IntaggRecipeProcedure(view, path, name)
			if err != nil || proc != state.Get(mapRef(ref).Address) {
				t.Fatal("capture retargeted by name", proc, err)
			}
		})
	}
}
