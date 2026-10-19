package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSQLRoutineFrozenPresentation(t *testing.T) {
	for _, call := range []string{"public.routine_recipe(1)", "public.sum(1)", "public.int4(1)", "public.int_array_enum(1)"} {
		t.Run(call, func(t *testing.T) {
			e := New()
			for _, name := range []string{"routine_recipe", "sum", "int4", "int_array_enum"} {
				run(t, e, "CREATE FUNCTION public."+name+"(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+1$$")
			}
			ctx := context.Background()
			b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			b.noParameters = true
			b.aggregateClause = "SELECT"
			syntax := statement(t, "SELECT "+call).(*parser.Select)
			raw := syntax.Targets[0]
			node, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			n, ok := node.(sqlRoutineExpression)
			if !ok {
				t.Fatalf("not a scalar SQL provider: %T", node)
			}
			if err := b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
				t.Fatal(err)
			}
			frozen, ok := b.normalizeStoredExpression(raw).(parser.FunctionExpr)
			if !ok || !frozen.StoredSQLRoutine {
				t.Fatal("missing owned SQL routine lowering")
			}
			if original, ok := raw.(parser.FunctionExpr); ok && original.StoredSQLRoutine {
				t.Fatal("mutated shared source AST")
			}
			ownedSyntax := *syntax
			ownedSyntax.Targets = []parser.Expr{frozen}
			project := &projectNode{querySchema: querySchema{{Name: "v", Usage: node.ResultUsage()}}, child: &singletonNode{}, expressions: []expr.Expr{node}, frame: b.frameID(), syntax: &ownedSyntax}
			definition := &viewDefinition{query: &BoundQuery{Root: project}, syntax: &ownedSyntax, dependencies: b.dependencies, routineRefs: b.viewRoutineRefs, literals: b.viewLiterals, castUsages: b.viewCastUsages}
			definition.frames, definition.queries = collectQueryFrames(project)
			read := e.CatalogRead()
			if err := definition.validateLiterals(read); err != nil {
				t.Fatal(err)
			}
			path := viewLiteralPath{query: b.frameID(), clause: viewTargetLiteral}
			if definition.routineRefs[path] != n.ref || len(definition.castUsages) != 1 {
				t.Fatal("lost selected identity or implicit argument conversion")
			}
			badReferences := []ids.ObjectRef{{}, New().catalog.Domain.Ref(n.ref.Epoch, n.ref.Address), n.ref.Domain().Ref(n.ref.Epoch+1, n.ref.Address)}
			subID := n.ref
			subID.Address.SubID = 1
			badReferences = append(badReferences, subID)
			builtins := read.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "abs")
			if len(builtins) == 0 {
				t.Fatal("missing native builtin control")
			}
			badReferences = append(badReferences, e.catalog.Domain.Ref(read.Epoch(), builtins[0]))
			for _, ref := range badReferences {
				bad := *definition
				bad.routineRefs = map[viewLiteralPath]ids.ObjectRef{path: ref}
				if err := bad.validateLiterals(read); err == nil {
					t.Fatal("forged frozen SQL reference accepted", ref)
				}
			}
			missing := *definition
			missing.routineRefs = nil
			if err := missing.validateLiterals(read); err == nil {
				t.Fatal("missing SQL capture accepted")
			}
			extra := *definition
			extra.routineRefs = map[viewLiteralPath]ids.ObjectRef{path: n.ref, {query: b.frameID(), target: 99}: n.ref}
			if err := extra.validateLiterals(read); err == nil {
				t.Fatal("extra SQL capture accepted")
			}
			unmarked := frozen
			unmarked.StoredSQLRoutine = false
			if _, err := definition.sqlRoutineRecipeProcedure(read, path, unmarked); err == nil {
				t.Fatal("unmarked SQL capture accepted")
			}
			render := func() string {
				t.Helper()
				renderer := newViewRenderer(definition)
				renderer.frame = b.frameID()
				text, err := renderer.expressionSQL(ctx, e.CatalogRead(), frozen, viewLiteralPath{clause: viewTargetLiteral})
				if err != nil {
					t.Fatal(err)
				}
				return text
			}
			if text := render(); !strings.HasPrefix(text, `"public"."`+n.procedure.Name()+`"(`) {
				t.Fatal("lost qualified frozen identity", text)
			}
			run(t, e, "ALTER SCHEMA public RENAME TO routine_recipe_moved")
			if text := render(); !strings.HasPrefix(text, `"routine_recipe_moved"."`+n.procedure.Name()+`"(`) {
				t.Fatal("rendered stale source namespace", text)
			}
			if frozen.Name.Parts[0].Text != "public" || definition.routineRefs[path] != n.ref {
				t.Fatal("render mutated frozen source or capture")
			}
		})
	}
}
