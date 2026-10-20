package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
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
			b.clause.aggregateClause = "SELECT"
			syntax := statement(t, "SELECT "+call).(*parser.Select)
			raw := syntax.Targets[0]
			node, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if err := b.captureViewLiterals(raw, node, ViewTargetLiteral, 0); err != nil {
				t.Fatal(err)
			}
			path := viewLiteralPath{Query: b.frameID(), Clause: ViewTargetLiteral}
			name := extension.IntaggRecipeFunction(raw)
			ref, ok := b.capture.RoutineRefs[path]
			if !ok || ref.Address == (ids.ObjectAddress{}) || len(b.capture.RoutineRefs) != 1 {
				t.Fatal("missing bound occurrence", b.capture.RoutineRefs)
			}
			var child plan.Node = &plan.SingletonNode{}
			if len(b.grouping.aggregates) != 0 {
				child = &plan.AggregateNode{Child: child, Calls: b.grouping.aggregates}
			}
			project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: node.ResultUsage()}}, Child: child, Expressions: []expr.Expr{node}, Frame: b.frameID(), Syntax: syntax}
			definition := &viewDefinition{query: &plan.Query{Root: project}, syntax: syntax, dependencies: b.dependencies, routineRefs: b.capture.RoutineRefs, literals: b.capture.Literals, castUsages: b.capture.CastUsages}
			definition.frames, definition.queries = collectQueryFrames(project)
			if err := definition.validateLiterals(e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			renderer := newViewRenderer(definition)
			renderer.frame = b.frameID()
			text, err := renderer.expressionSQL(ctx, e.CatalogRead(), raw, viewLiteralPath{Clause: ViewTargetLiteral})
			if err != nil || !strings.HasPrefix(text, `"public"."`+name+`"(`) {
				t.Fatal("unqualified bound recipe", text, err)
			}
			for _, badRef := range []ids.ObjectRef{{}, New().catalog.Domain.Ref(ref.Epoch, ref.Address), ref.Domain().Ref(ref.Epoch+1, ref.Address), ref.Domain().Ref(ref.Epoch, ext.Address())} {
				bad := *definition
				bad.routineRefs = map[viewLiteralPath]ids.ObjectRef{path: badRef}
				if err := bad.validateLiterals(e.CatalogRead()); err == nil {
					t.Fatal("forged frozen reference accepted", badRef)
				}
			}
			missing := *definition
			missing.routineRefs = nil
			if err := missing.validateLiterals(e.CatalogRead()); err == nil {
				t.Fatal("raw-name fallback accepted")
			}
			extra := *definition
			extra.routineRefs = map[viewLiteralPath]ids.ObjectRef{path: ref, {Query: b.frameID(), Target: 99}: ref}
			if err := extra.validateLiterals(e.CatalogRead()); err == nil {
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
			mapped, err := remapFrozenPresentation(view, definition, mapping, mapRef, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil)
			if err != nil {
				t.Fatal(err)
			}
			if mapped.routineRefs[path] != mapRef(ref) || definition.routineRefs[path] != ref {
				t.Fatal("capture remap mutated/lost authority")
			}
			if err := mapped.validateLiterals(view); err != nil {
				t.Fatal(err)
			}
			proc, err := mapped.intaggRecipeProcedure(view, path, name)
			if err != nil || proc != state.Get(mapRef(ref).Address) {
				t.Fatal("capture retargeted by name", proc, err)
			}
		})
	}
}
