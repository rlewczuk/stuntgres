package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
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
			b.Params.NoParameters = true
			b.Clause.AggregateClause = "SELECT"
			syntax := statement(t, "SELECT "+call).(*parser.Select)
			raw := syntax.Targets[0]
			node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			n, ok := node.(plan.SQLRoutineExpr)
			if !ok {
				t.Fatalf("not a scalar SQL provider: %T", node)
			}
			if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
				t.Fatal(err)
			}
			frozen, ok := b.NormalizeStoredExpression(raw).(parser.FunctionExpr)
			if !ok || !frozen.StoredSQLRoutine {
				t.Fatal("missing owned SQL routine lowering")
			}
			if original, ok := raw.(parser.FunctionExpr); ok && original.StoredSQLRoutine {
				t.Fatal("mutated shared source AST")
			}
			ownedSyntax := *syntax
			ownedSyntax.Targets = []parser.Expr{frozen}
			project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: node.ResultUsage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{node}, Frame: b.FrameID(), Syntax: &ownedSyntax}
			definition := NewView(ViewParts{Query: &plan.Query{Root: project}, Syntax: &ownedSyntax, Dependencies: b.Dependencies, RoutineRefs: b.Capture.RoutineRefs, Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages})
			definition = definition.With(func(p *ViewParts) { p.Frames, p.Queries = analyze.CollectQueryFrames(project) })
			read := e.CatalogRead()
			if err := definition.ValidateLiterals(read); err != nil {
				t.Fatal(err)
			}
			path := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral}
			if definition.RoutineRefs()[path] != n.Ref || len(definition.CastUsages()) != 1 {
				t.Fatal("lost selected identity or implicit argument conversion")
			}
			badReferences := []ids.ObjectRef{{}, New().catalog.Domain.Ref(n.Ref.Epoch, n.Ref.Address), n.Ref.Domain().Ref(n.Ref.Epoch+1, n.Ref.Address)}
			subID := n.Ref
			subID.Address.SubID = 1
			badReferences = append(badReferences, subID)
			builtins := read.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "abs")
			if len(builtins) == 0 {
				t.Fatal("missing native builtin control")
			}
			badReferences = append(badReferences, e.catalog.Domain.Ref(read.Epoch(), builtins[0]))
			for _, ref := range badReferences {
				bad := definition.With(func(p *ViewParts) { p.RoutineRefs = map[analyze.ViewLiteralPath]ids.ObjectRef{path: ref} })
				if err := bad.ValidateLiterals(read); err == nil {
					t.Fatal("forged frozen SQL reference accepted", ref)
				}
			}
			missing := definition.With(func(p *ViewParts) { p.RoutineRefs = nil })
			if err := missing.ValidateLiterals(read); err == nil {
				t.Fatal("missing SQL capture accepted")
			}
			extra := definition.With(func(p *ViewParts) {
				p.RoutineRefs = map[analyze.ViewLiteralPath]ids.ObjectRef{path: n.Ref, {Query: b.FrameID(), Target: 99}: n.Ref}
			})
			if err := extra.ValidateLiterals(read); err == nil {
				t.Fatal("extra SQL capture accepted")
			}
			unmarked := frozen
			unmarked.StoredSQLRoutine = false
			if _, err := definition.SQLRoutineRecipeProcedure(read, path, unmarked); err == nil {
				t.Fatal("unmarked SQL capture accepted")
			}
			render := func() string {
				t.Helper()
				renderer := NewQueryRenderer(definition)
				renderer.Frame = b.FrameID()
				text, err := renderer.ExpressionSQL(ctx, e.CatalogRead(), frozen, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
				if err != nil {
					t.Fatal(err)
				}
				return text
			}
			if text := render(); !strings.HasPrefix(text, `"public"."`+n.Procedure.Name()+`"(`) {
				t.Fatal("lost qualified frozen identity", text)
			}
			run(t, e, "ALTER SCHEMA public RENAME TO routine_recipe_moved")
			if text := render(); !strings.HasPrefix(text, `"routine_recipe_moved"."`+n.Procedure.Name()+`"(`) {
				t.Fatal("rendered stale source namespace", text)
			}
			if frozen.Name.Parts[0].Text != "public" || definition.RoutineRefs()[path] != n.Ref {
				t.Fatal("render mutated frozen source or capture")
			}
		})
	}
}
