package core_test

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineFrozenPresentation(t *testing.T) {
	for _, call := range []string{"public.routine_recipe(1)", "public.sum(1)", "public.int4(1)", "public.int_array_enum(1)"} {
		t.Run(call, func(t *testing.T) {
			e := engine.New()
			for _, name := range []string{"routine_recipe", "sum", "int4", "int_array_enum"} {
				enginetest.Run(t, e, "CREATE FUNCTION public."+name+"(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+1$$")
			}
			ctx := context.Background()
			b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			b.Params.NoParameters = true
			b.Clause.AggregateClause = "SELECT"
			syntax := enginetest.Statement(t, "SELECT "+call).(*parser.Select)
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
			definition := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: project}, Syntax: &ownedSyntax, Dependencies: b.Dependencies, RoutineRefs: b.Capture.RoutineRefs, Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages})
			definition = definition.With(func(p *stored.ViewParts) { p.Frames, p.Queries = analyze.CollectQueryFrames(project) })
			read := e.CatalogRead()
			if err := definition.ValidateLiterals(read); err != nil {
				t.Fatal(err)
			}
			path := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral}
			if definition.RoutineRefs()[path] != n.Ref || len(definition.CastUsages()) != 1 {
				t.Fatal("lost selected identity or implicit argument conversion")
			}
			badReferences := []ids.ObjectRef{{}, engine.New().Catalog.Domain.Ref(n.Ref.Epoch, n.Ref.Address), n.Ref.Domain().Ref(n.Ref.Epoch+1, n.Ref.Address)}
			subID := n.Ref
			subID.Address.SubID = 1
			badReferences = append(badReferences, subID)
			builtins := read.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "abs")
			if len(builtins) == 0 {
				t.Fatal("missing native builtin control")
			}
			badReferences = append(badReferences, e.Catalog.Domain.Ref(read.Epoch(), builtins[0]))
			for _, ref := range badReferences {
				bad := definition.With(func(p *stored.ViewParts) { p.RoutineRefs = map[analyze.ViewLiteralPath]ids.ObjectRef{path: ref} })
				if err := bad.ValidateLiterals(read); err == nil {
					t.Fatal("forged frozen SQL reference accepted", ref)
				}
			}
			missing := definition.With(func(p *stored.ViewParts) { p.RoutineRefs = nil })
			if err := missing.ValidateLiterals(read); err == nil {
				t.Fatal("missing SQL capture accepted")
			}
			extra := definition.With(func(p *stored.ViewParts) {
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
				renderer := stored.NewQueryRenderer(definition)
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
			enginetest.Run(t, e, "ALTER SCHEMA public RENAME TO routine_recipe_moved")
			if text := render(); !strings.HasPrefix(text, `"routine_recipe_moved"."`+n.Procedure.Name()+`"(`) {
				t.Fatal("rendered stale source namespace", text)
			}
			if frozen.Name.Parts[0].Text != "public" || definition.RoutineRefs()[path] != n.Ref {
				t.Fatal("render mutated frozen source or capture")
			}
		})
	}
	for _, tc := range []struct {
		name, body string
		clause analyze.ViewLiteralClause
	}{
		{"main-values", "WITH s AS MATERIALIZED (SELECT v FROM presentation_source) UPDATE presentation_target t SET v=s.v FROM s JOIN (VALUES (1)) AS x(n) ON x.n=1 WHERE t.id=x.n RETURNING t.v", analyze.ViewValuesCellLiteral},
		{"child-values", "WITH s AS MATERIALIZED (SELECT v FROM presentation_source), m AS (UPDATE presentation_target t SET v=s.v FROM s JOIN (VALUES (1)) AS x(n) ON x.n=1 WHERE t.id=x.n RETURNING t.v) SELECT v FROM m", analyze.ViewValuesCellLiteral},
		{"main-function", "WITH s AS MATERIALIZED (SELECT v FROM presentation_source) UPDATE presentation_target t SET v=s.v FROM s CROSS JOIN generate_series(1,1) AS x(n) WHERE t.id=x.n RETURNING t.v", analyze.ViewFunctionLiteral},
		{"child-function", "WITH s AS MATERIALIZED (SELECT v FROM presentation_source), m AS (UPDATE presentation_target t SET v=s.v FROM s CROSS JOIN generate_series(1,1) AS x(n) WHERE t.id=x.n RETURNING t.v) SELECT v FROM m", analyze.ViewFunctionLiteral},
		{"delete-values", "WITH s AS MATERIALIZED (SELECT v FROM presentation_source) DELETE FROM presentation_target t USING s JOIN (VALUES (1)) AS x(n) ON x.n=1 WHERE t.id=x.n RETURNING t.v", analyze.ViewValuesCellLiteral},
		{"child-source-only", "WITH m AS (UPDATE presentation_target t SET v=s.v FROM presentation_source s CROSS JOIN presentation_other o CROSS JOIN (VALUES (1)) AS x(n) WHERE t.id=x.n AND o.v>0 RETURNING t.v) SELECT v FROM m", analyze.ViewValuesCellLiteral},
		{"main-values-dead", "WITH dead AS MATERIALIZED (SELECT v FROM presentation_dead), s AS MATERIALIZED (SELECT v FROM presentation_source) UPDATE presentation_target t SET v=s.v FROM s CROSS JOIN (VALUES (1)) AS x(n) WHERE t.id=x.n RETURNING t.v", analyze.ViewValuesCellLiteral},
		{"child-values-dead", "WITH dead AS MATERIALIZED (SELECT v FROM presentation_dead), s AS MATERIALIZED (SELECT v FROM presentation_source), m AS (UPDATE presentation_target t SET v=s.v FROM s CROSS JOIN (VALUES (1)) AS x(n) WHERE t.id=x.n RETURNING t.v) SELECT v FROM m", analyze.ViewValuesCellLiteral},
	} {
		t.Run("raw-owners/"+tc.name, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE presentation_source(unused integer,v integer,tail integer)")
			enginetest.Run(t, e, "CREATE TABLE presentation_target(id integer,v integer)")
			enginetest.Run(t, e, "CREATE TABLE presentation_other(unused integer,v integer,tail integer)")
			enginetest.Run(t, e, "CREATE TABLE presentation_dead(v integer)")
			enginetest.Run(t, e, "CREATE FUNCTION presentation_owner() RETURNS integer LANGUAGE SQL BEGIN ATOMIC "+tc.body+"; END")
			read := e.CatalogRead()
			addresses := read.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "presentation_owner")
			if len(addresses) != 1 {
				t.Fatal("missing bound routine")
			}
			procedure, ok := read.Procedure(addresses[0])
			if !ok {
				t.Fatal("missing bound procedure")
			}
			program, ok := procedure.SQLBody.(*stored.RoutineProgram)
			if !ok {
				t.Fatal("missing frozen routine program")
			}
			action := program.Action(0)
			binding := action.Binding()
			checkDemand := func(body analyze.Binding) (live, dead int) {
				t.Helper()
				for _, source := range body.QuerySources {
					if source.Relation.Name() == "presentation_dead" {
						dead++
						if !source.AnalysisOnly {
							t.Fatal("unused SELECT CTE acquired executable source demand")
						}
					} else {
						live++
						if source.AnalysisOnly {
							t.Fatal("genuine mutation source retained only an analysis header", source.Relation.Name())
						}
					}
				}
				return live, dead
			}
			live, dead := checkDemand(binding)
			if live == 0 || strings.HasSuffix(tc.name, "-dead") && dead != 1 {
				t.Fatal("missing actual live/dead source-demand witness", live, dead)
			}
			if err := stored.VisitRuleModifiers(binding, func(_ *plan.CTEDefinition, mutation *analyze.Binding) error {
				checkDemand(*mutation)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			sources := slices.Clone(binding.QuerySources)
			before := binding.Capture
			before.Literals, before.CastUsages = maps.Clone(before.Literals), maps.Clone(before.CastUsages)
			before.RoutineRefs, before.OperatorRefs = maps.Clone(before.RoutineRefs), maps.Clone(before.OperatorRefs)
			before.Composites = maps.Clone(before.Composites)
			before.SimilarSubstrings, before.PosixSubstrings = maps.Clone(before.SimilarSubstrings), maps.Clone(before.PosixSubstrings)
			before.Collations, before.Columns, before.Stars = maps.Clone(before.Collations), maps.Clone(before.Columns), maps.Clone(before.Stars)
			dependencies := slices.Clone(binding.Dependencies)
			frozen, err := view.FreezeRoutineAction(read, ids.SessionToken{}, action.Syntax(), binding)
			if err != nil {
				t.Fatal("refreeze actual binding", err)
			}
			if !reflect.DeepEqual(before, binding.Capture) || !reflect.DeepEqual(dependencies, binding.Dependencies) || !reflect.DeepEqual(sources, binding.QuerySources) {
				t.Fatal("presentation projection changed original capture authority")
			}
			presentations := []*stored.View{frozen.Presentation()}
			for _, modifier := range frozen.ModifierPresentations() {
				presentations = append(presentations, modifier)
			}
			shared := assertRoutineOccurrenceOwners(t, before, presentations)
			if (tc.name == "child-values" || tc.name == "child-function") && !shared {
				t.Fatal("shared SELECT-CTE capture lost its overlapping owners")
			}
			var occurrence analyze.ViewLiteralPath
			found := false
			for _, presentation := range presentations {
				if err := presentation.ValidateLiterals(read); err != nil {
					t.Fatal("raw presentation literals", err)
				}
				for path := range presentation.Literals() {
					if path.Query == 1 && path.Clause == tc.clause {
						occurrence, found = path, true
					}
				}
			}
			if !found {
				t.Fatal("genuine frame1 source capture lost")
			}
			checked := stored.NewRoutineProgram([]stored.RuleAction{frozen})
			if err := checked.Validate(read); err != nil {
				t.Fatal("complete presentation association", err)
			}
			if tc.name == "child-source-only" {
				// The SELECT scan cannot itself reach mutation-owned FROM
				// sources. Generic view validation remains locally complete;
				// the checked action above proves the exact associated union.
				if err := stored.ValidateViewLeaves(read, frozen.Presentation()); err == nil {
					t.Fatal("ordinary per-view manifest validation was weakened")
				}
				assertRoutineSourceManifestCorruptions(t, read, frozen)
			}
			text, err := checked.StatementSQL(context.Background(), read, 0)
			if err != nil || text == "" {
				t.Fatal("checked presentation rendering", text, err)
			}
			enginetest.Run(t, e, "CREATE FUNCTION presentation_replay() RETURNS integer LANGUAGE SQL BEGIN ATOMIC "+strings.TrimSuffix(text, ";")+"; END")
			for _, corruption := range []string{"missing", "extra", "wrong-path"} {
				badBinding := binding
				badBinding.Capture.Literals = maps.Clone(binding.Capture.Literals)
				value := badBinding.Capture.Literals[occurrence]
				switch corruption {
				case "missing":
					delete(badBinding.Capture.Literals, occurrence)
				case "extra":
					extra := occurrence
					extra.Target = 999
					badBinding.Capture.Literals[extra] = value
				case "wrong-path":
					delete(badBinding.Capture.Literals, occurrence)
					occurrence.Node += 999
					badBinding.Capture.Literals[occurrence] = value
				}
				bad, err := view.FreezeRoutineAction(read, ids.SessionToken{}, action.Syntax(), badBinding)
				if err == nil {
					err = stored.NewRoutineProgram([]stored.RuleAction{bad}).Validate(read)
				}
				if err == nil {
					t.Fatal("corrupt raw occurrence admitted", corruption)
				}
			}
		})
	}
}

func assertRoutineOccurrenceOwners(t *testing.T, capture analyze.ViewCapture, presentations []*stored.View) bool {
	t.Helper()
	shared := false
	for _, name := range []string{"Literals", "CastUsages", "RoutineRefs", "OperatorRefs", "Composites", "SimilarSubstrings", "PosixSubstrings", "Collations", "Columns", "Stars"} {
		original := reflect.ValueOf(capture).FieldByName(name)
		keys := original.MapRange()
		for keys.Next() {
			owners := 0
			for _, presentation := range presentations {
				projected := reflect.ValueOf(presentation.Parts()).FieldByName(name)
				value := projected.MapIndex(keys.Key())
				if value.IsValid() {
					owners++
					if !reflect.DeepEqual(value.Interface(), keys.Value().Interface()) {
						t.Fatal("projection changed captured value", name, keys.Key())
					}
				}
			}
			if owners == 0 {
				t.Fatal("capture key has no raw owner", name, keys.Key())
			}
			shared = shared || owners > 1
		}
		for _, presentation := range presentations {
			parts := presentation.Parts()
			roots, err := presentation.LiteralRoots()
			if err != nil {
				t.Fatal(err)
			}
			projected := reflect.ValueOf(parts).FieldByName(name).MapRange()
			for projected.Next() {
				path := projected.Key().Interface().(analyze.ViewLiteralPath)
				path.Node, path.Argument = 0, 0
				if !roots[path] {
					t.Fatal("synthetic carrier or absent syntax acquired a raw occurrence", name, path)
				}
			}
		}
	}
	return shared
}

func assertRoutineSourceManifestCorruptions(t *testing.T, read catalog.Read, action stored.RuleAction) {
	t.Helper()
	for _, kind := range []string{"absent", "extra", "swapped", "reference", "attribute", "missing-child", "extra-child"} {
		bad := action.With(func(parts *stored.RuleActionParts) {
			parts.ModifierPresentations = maps.Clone(parts.ModifierPresentations)
			edit := func(view *stored.View) *stored.View {
				return view.With(func(p *stored.ViewParts) {
					p.Sources = slices.Clone(p.Sources)
					switch kind {
					case "absent", "missing-child":
						p.Sources = nil
					case "extra", "extra-child":
						p.Sources = append(p.Sources, p.Sources[0])
					case "swapped":
						p.Sources[0], p.Sources[1] = p.Sources[1], p.Sources[0]
					case "reference":
						p.Sources[0].Ref = ids.ObjectRef{}
					case "attribute":
						p.Sources[0].Attributes = slices.Clone(p.Sources[0].Attributes)
						p.Sources[0].Attributes[0] = 999
					}
				})
			}
			if kind == "absent" || kind == "extra" {
				parts.Presentation = edit(parts.Presentation)
			} else {
				for statement, presentation := range parts.ModifierPresentations {
					parts.ModifierPresentations[statement] = edit(presentation)
				}
			}
		})
		program := stored.NewRoutineProgram([]stored.RuleAction{bad})
		if err := program.Validate(read); err == nil {
			t.Fatal("corrupt complete source manifest admitted", kind)
		}
		if _, err := program.StatementSQL(context.Background(), read, 0); err == nil {
			t.Fatal("corrupt complete source manifest rendered", kind)
		}
		if _, err := program.Rebind(read, nil, stored.NewRoutineRemap(read, nil, nil, nil, false)); err == nil {
			t.Fatal("corrupt complete source manifest published", kind)
		}
	}
}
