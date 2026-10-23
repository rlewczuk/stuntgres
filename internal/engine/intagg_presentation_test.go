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
			b.NormalizeStoredQuerySyntax(syntax)
			raw = syntax.Targets[0]
			path := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral}
			ref, ok := b.Capture.RoutineRefs[path]
			if !ok || ref.Address == (ids.ObjectAddress{}) || len(b.Capture.RoutineRefs) != 1 {
				t.Fatal("missing bound occurrence", b.Capture.RoutineRefs)
			}
			name, err := extension.ProcedureSymbol(e.CatalogRead(), e.CatalogRead().MustProcedure(ref.Address))
			if err != nil {
				t.Fatal(err)
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
			// A retained occurrence follows its installed slot, not the old
			// spelling or the extension's installation namespace.
			renamedState := catalog.CloneState(e.catalog.state)
			original := renamedState.MustProcedure(ref.Address)
			renamed := *original
			renamed.ObjectName = "intagg_current_recipe"
			renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
			renamed.ObjectRevision++
			renamedState.UnbindName(catalog.DescriptorNameKey(original))
			renamedState.SetDescriptor(renamed.Address(), &renamed)
			renamedState.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
			renamedState.SetProcedureNames(catalog.RebuildProcedureNames(renamedState.Descriptors()))
			renamedView := e.catalog.read(renamedState)
			if err := definition.ValidateLiterals(renamedView); err != nil {
				t.Fatal(err)
			}
			text, err = renderer.ExpressionSQL(ctx, renamedView, raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
			if err != nil || !strings.HasPrefix(text, `"pg_catalog"."intagg_current_recipe"(`) {
				t.Fatal("intagg occurrence followed stale spelling", text, err)
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
			mapped, err := stored.RemapView(view, definition, mapping, mapRef, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil)
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

func TestProviderRoutineOccurrenceIdentity(t *testing.T) {
	for _, test := range []struct {
		name, expression string
		installed        bool
	}{
		{"builtin scalar", "abs(4)", false},
		{"builtin aggregate", "sum(2)", false},
		{"builtin window", "row_number() OVER ()", false},
		{"SQL extract syntax", "EXTRACT(YEAR FROM DATE '2020-01-01')", false},
		{"SQL substring syntax", "SUBSTRING('abc' FROM 1 FOR 2)", false},
		{"installed scalar", "digest('abc', 'sha256')", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := New()
			if test.installed {
				e, _ = pgcryptoUnitInstalled(t)
			}
			ctx := context.Background()
			b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			b.Clause.AggregateClause = "SELECT"
			syntax := statement(t, "SELECT "+test.expression).(*parser.Select)
			written := syntax.Targets[0]
			node, err := b.Bind(syntax.Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if err := b.CaptureViewLiterals(syntax.Targets[0], node, analyze.ViewTargetLiteral, 0); err != nil {
				t.Fatal(err)
			}
			b.NormalizeStoredQuerySyntax(syntax)
			path := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral}
			ref, exists := b.Capture.RoutineRefs[path]
			if !exists || len(b.Capture.RoutineRefs) != 1 {
				t.Fatal("provider occurrence missing", b.Capture.RoutineRefs)
			}
			var child plan.Node = &plan.SingletonNode{}
			if len(b.Grouping.Aggregates) != 0 {
				child = &plan.AggregateNode{Child: child, Calls: b.Grouping.Aggregates}
			}
			if len(b.Grouping.WindowCalls) != 0 {
				child = &plan.WindowNode{Child: child, Calls: b.Grouping.WindowCalls}
			}
			project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: node.ResultUsage()}}, Child: child, Expressions: []expr.Expr{node}, Frame: b.FrameID(), Syntax: syntax}
			definition := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: project}, Syntax: syntax, Dependencies: b.Dependencies, RoutineRefs: b.Capture.RoutineRefs, Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages})
			definition = definition.With(func(p *stored.ViewParts) { p.Frames, p.Queries = analyze.CollectQueryFrames(project) })
			if err := definition.ValidateLiterals(e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			raw := syntax.Targets[0]
			switch call := raw.(type) {
			case parser.FunctionExpr:
				if source, ok := written.(parser.FunctionExpr); ok && (catalog.ObjectNameText(source.Name) != catalog.ObjectNameText(call.Name) || source.Syntax != call.Syntax || source.StringSyntax != call.StringSyntax || source.Variadic != call.Variadic) {
					t.Fatal("provider identity annotation erased written function syntax")
				}
				if call.StoredProviderSymbol == "" {
					t.Fatal("missing independent immutable symbol")
				}
				for _, symbol := range []string{"", "substituted_provider"} {
					badCall := call
					badCall.StoredProviderSymbol = symbol
					badSyntax := *syntax
					badSyntax.Targets = []parser.Expr{badCall}
					badProject := *project
					badProject.Syntax = &badSyntax
					bad := definition.With(func(p *stored.ViewParts) {
						p.Syntax = &badSyntax
						p.Frames = map[uint32]*plan.ProjectNode{b.FrameID(): &badProject}
					})
					if err := bad.ValidateLiterals(e.CatalogRead()); err == nil {
						t.Fatal("missing or substituted symbol admitted")
					}
				}
			}
			state := catalog.CloneState(e.catalog.state)
			original := state.MustProcedure(ref.Address)
			renamed := *original
			renamed.ObjectName = "current_provider_recipe"
			renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			if test.installed {
				renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
			}
			renamed.ObjectRevision++
			state.UnbindName(catalog.DescriptorNameKey(original))
			state.SetDescriptor(renamed.Address(), &renamed)
			state.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
			state.SetProcedureNames(catalog.RebuildProcedureNames(state.Descriptors()))
			read := e.catalog.read(state)
			if err := definition.ValidateLiterals(read); err != nil {
				t.Fatal(err)
			}
			renderer := stored.NewQueryRenderer(definition)
			renderer.Frame = b.FrameID()
			text, err := renderer.ExpressionSQL(ctx, read, raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
			if err != nil || !strings.Contains(text, `."current_provider_recipe"(`) {
				t.Fatal("provider occurrence did not follow captured identity", text, err)
			}
			subID := ref
			subID.Address.SubID = 1
			for _, badRef := range []ids.ObjectRef{{}, New().catalog.Domain.Ref(ref.Epoch, ref.Address), ref.Domain().Ref(ref.Epoch+1, ref.Address), subID} {
				bad := definition.With(func(p *stored.ViewParts) { p.RoutineRefs = map[analyze.ViewLiteralPath]ids.ObjectRef{path: badRef} })
				if err := bad.ValidateLiterals(read); err == nil {
					t.Fatal("forged provider occurrence accepted", badRef)
				}
			}
			otherIdentity, ok := catalog.BuiltinRoutineIdentityBySignature("sqrt", "701")
			if !ok {
				t.Fatal("released sqrt identity missing")
			}
			other := read.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: otherIdentity.ID})
			otherRef := ref.Domain().Ref(ref.Epoch, other.Address())
			wrongProvider := definition.With(func(p *stored.ViewParts) {
				p.RoutineRefs = map[analyze.ViewLiteralPath]ids.ObjectRef{path: otherRef}
				p.Dependencies = append(p.Dependencies[:len(p.Dependencies):len(p.Dependencies)], p.Dependencies[0])
				p.Dependencies[len(p.Dependencies)-1].Ref = otherRef
				p.Dependencies[len(p.Dependencies)-1].Revision = other.Revision()
			})
			if err := wrongProvider.ValidateLiterals(read); err == nil {
				t.Fatal("different released provider accepted despite a valid dependency")
			}
			missing := definition.With(func(p *stored.ViewParts) { p.RoutineRefs = nil })
			if err := missing.ValidateLiterals(read); err == nil {
				t.Fatal("missing provider occurrence accepted")
			}
			noDependency := definition.With(func(p *stored.ViewParts) { p.Dependencies = nil })
			if err := noDependency.ValidateLiterals(read); err == nil {
				t.Fatal("provider occurrence without dependency accepted")
			}
			extra := definition.With(func(p *stored.ViewParts) {
				p.RoutineRefs = map[analyze.ViewLiteralPath]ids.ObjectRef{path: ref, {Query: b.FrameID(), Target: 99}: ref}
			})
			if err := extra.ValidateLiterals(read); err == nil {
				t.Fatal("unused provider occurrence accepted")
			}
		})
	}
}
