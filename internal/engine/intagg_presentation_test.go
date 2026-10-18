package engine

import (
	"context"
	"strings"
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
			b.aggregateClause = "SELECT"
			syntax := statement(t, "SELECT "+call).(*parser.Select)
			raw := syntax.Targets[0]
			node, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if err := b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
				t.Fatal(err)
			}
			path := viewLiteralPath{query: b.frameID(), clause: viewTargetLiteral}
			name := intaggRecipeFunction(raw)
			ref, ok := b.viewIntaggRefs[path]
			if !ok || ref.Address == (ids.ObjectAddress{}) || len(b.viewIntaggRefs) != 1 {
				t.Fatal("missing bound occurrence", b.viewIntaggRefs)
			}
			var child LogicalNode = &singletonNode{}
			if len(b.aggregates) != 0 {
				child = &aggregateNode{child: child, calls: b.aggregates}
			}
			project := &projectNode{querySchema: querySchema{{Name: "v", Usage: node.ResultUsage()}}, child: child, expressions: []expr.Expr{node}, frame: b.frameID(), syntax: syntax}
			definition := &viewDefinition{query: &BoundQuery{Root: project}, syntax: syntax, dependencies: b.dependencies, intaggRefs: b.viewIntaggRefs, literals: b.viewLiterals, castUsages: b.viewCastUsages}
			definition.frames, definition.queries = collectQueryFrames(project)
			if err := definition.validateLiterals(e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			renderer := newViewRenderer(definition)
			renderer.frame = b.frameID()
			text, err := renderer.expressionSQL(ctx, e.CatalogRead(), raw, viewLiteralPath{clause: viewTargetLiteral})
			if err != nil || !strings.HasPrefix(text, `"public"."`+name+`"(`) {
				t.Fatal("unqualified bound recipe", text, err)
			}
			for _, badRef := range []ids.ObjectRef{{}, New().catalog.Domain.Ref(ref.Epoch, ref.Address), ref.Domain().Ref(ref.Epoch+1, ref.Address), ref.Domain().Ref(ref.Epoch, ext.Address())} {
				bad := *definition
				bad.intaggRefs = map[viewLiteralPath]ids.ObjectRef{path: badRef}
				if err := bad.validateLiterals(e.CatalogRead()); err == nil {
					t.Fatal("forged frozen reference accepted", badRef)
				}
			}
			missing := *definition
			missing.intaggRefs = nil
			if err := missing.validateLiterals(e.CatalogRead()); err == nil {
				t.Fatal("raw-name fallback accepted")
			}
			extra := *definition
			extra.intaggRefs = map[viewLiteralPath]ids.ObjectRef{path: ref, {query: b.frameID(), target: 99}: ref}
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
				d := remapExtensionDescriptor(e.catalog.state.Get(address), mapping)
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
			if mapped.intaggRefs[path] != mapRef(ref) || definition.intaggRefs[path] != ref {
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
