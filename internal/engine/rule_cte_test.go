package engine

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRuleCTECommandNormalization(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	t.Run("expanded_provenance", func(t *testing.T) {
		for _, test := range []struct {
			name     string
			commands []view.RuleCommand
			message  string
		}{
			{"single_select", []view.RuleCommand{{Syntax: &parser.Select{}, Source: view.RuleInstead}}, ""},
			{"single_original", []view.RuleCommand{{Source: view.RuleOriginal}}, ""},
			{"nothing", nil, "DO INSTEAD NOTHING"},
			{"conditional", []view.RuleCommand{{Source: view.RuleConditionalInstead}, {Source: view.RuleInstead}}, "conditional DO INSTEAD"},
			{"recursive_also", []view.RuleCommand{{Source: view.RuleInstead}, {Source: view.RuleAlso}}, "DO ALSO"},
			{"recursive_multi", []view.RuleCommand{{Source: view.RuleInstead}, {Source: view.RuleInstead}}, "multi-statement DO INSTEAD"},
		} {
			t.Run(test.name, func(t *testing.T) {
				err := view.RuleCTEProgramError(view.RuleProgram{Commands: test.commands})
				if test.message == "" {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), test.message) {
					t.Fatalf("diagnostic=%v want %s", err, test.message)
				}
			})
		}
	})
	original := ruleUnitInsert(e, src, 1, 10)
	originalBinding := original.Binding()
	definition := &plan.CTEDefinition{Statement: original.Syntax(), Mutation: &originalBinding, Schema: plan.QuerySchema(src.Columns), Owner: 1}
	t.Run("complete_executable_tuple", func(t *testing.T) {
		action := ruleUnitInsert(e, sink, 1, 11)
		src.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 900101, Name: "redirect", Event: "INSERT", Instead: true, Actions: []stored.RuleAction{action}})})
		defer func() { src.Rules = nil }()
		parent := analyze.Binding{CTEs: []*plan.CTEDefinition{definition}}
		e.mu.Lock()
		err := view.NormalizeRuleCTEsLocked(e.catalog.owner(), auth, &parser.Select{}, &parent)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		rewrite := view.RuleCTEsOf(parent.Rule.CTEs)[definition]
		if rewrite == nil || rewrite.Definition != definition || rewrite.Definition.Statement != original.Syntax() || rewrite.Definition.Mutation != &originalBinding || rewrite.Command.Syntax != action.Syntax() || rewrite.Command.Binding.Ref != action.Binding().Ref || rewrite.Mutation != -1 {
			t.Fatal("normalization did not preserve the provider and select the executable target")
		}
		if definition.Query != nil {
			t.Fatal("modifier acquired a SELECT provider")
		}
	})
	t.Run("protected_modifier_and_select", func(t *testing.T) {
		provider := &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(src.Columns), Frame: 2, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{ruleUnitInt(src, 1), ruleUnitInt(src, 10)}}}
		selection := &plan.CTEDefinition{Query: provider, Owner: 1}
		for _, protected := range []*plan.CTEDefinition{definition, selection} {
			scan := &plan.DerivedNode{QuerySchema: plan.QuerySchema(src.Columns), Query: protected.Query, CTE: protected, ScanFrame: 1}
			mapped, err := stored.NewRuleTreeMapper(auth.Catalog, func(frame uint32) uint32 { return frame + 7 }, 4, []*plan.CTEDefinition{protected}).Query(scan)
			if err != nil {
				t.Fatal(err)
			}
			result := mapped.(*plan.DerivedNode)
			if result.CTE != protected || result.Query != protected.Query || result.CTE.Owner != 1 || result.ScanFrame != 8 {
				t.Fatal("protected producer identity/owner/query graph changed")
			}
		}
	})
	t.Run("action_owner_and_project_memo", func(t *testing.T) {
		leaf := &plan.SourceNode{QuerySchema: plan.QuerySchema(sink.Columns), Occurrence: 1}
		provider := &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(sink.Columns), Frame: 2, Child: leaf, Expressions: []expr.Expr{ruleUnitInt(sink, 1), ruleUnitInt(sink, 11)}}}
		local := &plan.CTEDefinition{Query: provider, Owner: 1}
		scan := &plan.DerivedNode{QuerySchema: plan.QuerySchema(sink.Columns), Query: provider, CTE: local, ScanFrame: 1}
		action := analyze.Binding{CTEs: []*plan.CTEDefinition{local}, Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(sink.Columns), Frame: 1, Child: scan, Expressions: provider.Root.(*plan.ProjectNode).Expressions, AnalysisExpressions: []expr.Expr{plan.SubqueryExpr{QueryPlan: provider, CTEEdge: true}}}}}
		parent := original.Binding()
		parent.CTEs = []*plan.CTEDefinition{definition}
		mapped, err := view.IsolateRuleCTEFrames(auth.Catalog, parent, action, &parser.Insert{})
		if err != nil {
			t.Fatal(err)
		}
		mapped, err = view.OffsetRuleSources(auth.Catalog, mapped, 3, parent.CTEs)
		if err != nil {
			t.Fatal(err)
		}
		project := mapped.Query.Root.(*plan.ProjectNode)
		result := project.Child.(*plan.DerivedNode)
		analysis := project.AnalysisExpressions[0].(plan.SubqueryExpr)
		if result.CTE == local || result.CTE.Owner == definition.Owner || result.ScanFrame != 1 || result.Query != result.CTE.Query || mapped.CTEs[0] != result.CTE || analysis.QueryPlan.Root != result.CTE.Query.Root {
			t.Fatal("action provider mapping is incoherent or shadows protected owner")
		}
		if result.Query.Root.(*plan.ProjectNode).Frame == 2 || result.Query.Root.(*plan.ProjectNode).Child.(*plan.SourceNode).Occurrence != 4 {
			t.Fatal("provider frame/occurrence was not mapped exactly once")
		}
		final, err := view.FinalizeRuleQuery(auth.Catalog, project)
		if err != nil {
			t.Fatal(err)
		}
		finalScan := final.(*plan.ProjectNode).Child.(*plan.DerivedNode)
		if finalScan.CTE != result.CTE || finalScan.Query != result.CTE.Query {
			t.Fatal("finalization forked immutable provider graph")
		}
		if local.Owner != 1 || provider.Root.(*plan.ProjectNode).Frame != 2 || leaf.Occurrence != 1 {
			t.Fatal("mutated frozen action template")
		}
	})
	t.Run("top_owned_namespace_only", func(t *testing.T) {
		position := parser.Position{Offset: 17}
		raw := parser.CommonTableExpression{Name: parser.Identifier{Text: "q", Pos: position}}
		newColumn := plan.ColumnExpr{Info: view.RuleColumnInfo(src.Columns[0]), Scope: analyze.RulePseudoScope, Frame: 2}
		oldColumn := newColumn
		oldColumn.Frame = 1
		provider := &plan.Query{Root: &plan.ProjectNode{Expressions: []expr.Expr{newColumn, oldColumn}}}
		local := &plan.CTEDefinition{Query: provider, Position: position}
		body := analyze.Binding{CTEs: []*plan.CTEDefinition{local}, Query: &plan.Query{Root: &plan.ProjectNode{AnalysisExpressions: []expr.Expr{plan.SubqueryExpr{Info: expr.Info{Pos: position}, QueryPlan: provider, CTEEdge: true}}}}}
		for _, action := range []parser.Statement{
			&parser.Insert{With: []parser.CommonTableExpression{raw}},
			&parser.Insert{Source: &parser.InsertSource{Select: &parser.Select{With: []parser.CommonTableExpression{raw}}}},
		} {
			err := view.ValidateRuleActionCTEs(action, body)
			if err == nil || !strings.Contains(err.Error(), "cannot refer to OLD within WITH query") {
				t.Fatalf("OLD priority/owned scope: %v", err)
			}
		}
		if err := view.ValidateRuleActionCTEs(&parser.Insert{}, body); err != nil {
			t.Fatalf("nested/enclosing WITH was rejected: %v", err)
		}
		provider.Root = &plan.ProjectNode{Expressions: []expr.Expr{plan.RowExpr{RuleSide: 2}}}
		if err := view.ValidateRuleActionCTEs(&parser.Insert{With: []parser.CommonTableExpression{raw}}, body); err == nil || !strings.Contains(err.Error(), "cannot refer to NEW within WITH query") {
			t.Fatalf("empty formal whole row lost identity: %v", err)
		}
	})
	t.Run("semantic_provider_memo_and_finalization", func(t *testing.T) {
		pseudo := plan.ColumnExpr{Info: view.RuleColumnInfo(src.Columns[0]), Scope: analyze.RulePseudoScope, Frame: 2}
		provider := &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(sink.Columns), Frame: 2, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{pseudo, ruleUnitInt(sink, 10)}}}
		local := &plan.CTEDefinition{Query: provider, Owner: 1}
		scan := &plan.DerivedNode{QuerySchema: plan.QuerySchema(sink.Columns), CTE: local, Query: provider, ScanFrame: 1}
		root := &plan.ProjectNode{QuerySchema: plan.QuerySchema(sink.Columns), Frame: 1, Child: scan, Expressions: []expr.Expr{ruleUnitInt(sink, 1), ruleUnitInt(sink, 10)}, AnalysisExpressions: []expr.Expr{plan.SubqueryExpr{QueryPlan: provider, CTEEdge: true}}}
		finalized := make(map[plan.Node]bool)
		finishCount := 0
		mapper := stored.NewRuleTreeMapper(auth.Catalog, nil, 0, nil, stored.RuleTreeMapping{
			Leaf: func(expr expr.Expr) (expr.Expr, bool, error) {
				if column, ok := expr.(plan.ColumnExpr); ok && column.Scope == analyze.RulePseudoScope {
					return ruleUnitInt(sink, 11), true, nil
				}
				return expr, false, nil
			},
			Finish: func(root plan.Node) (plan.Node, error) {
				finishCount++
				result, err := view.FinalizeRuleQuery(auth.Catalog, root, finalized)
				if err == nil {
					finalized[result] = true
				}
				return result, err
			},
		})
		mapped, err := mapper.Query(root)
		if err != nil {
			t.Fatal(err)
		}
		project := mapped.(*plan.ProjectNode)
		result := project.Child.(*plan.DerivedNode)
		analysis := project.AnalysisExpressions[0].(plan.SubqueryExpr)
		definition, err := mapper.Definition(local)
		if err != nil {
			t.Fatal(err)
		}
		if definition != result.CTE || result.Query != definition.Query || analysis.QueryPlan.Root != definition.Query.Root {
			t.Fatal("semantic graft forked definition/scan/analysis project")
		}
		if _, formal := definition.Query.Root.(*plan.ProjectNode).Expressions[0].(plan.ColumnExpr); formal {
			t.Fatal("formal reference survived semantic provider mapping")
		}
		repeated, err := mapper.Query(root)
		if err != nil || repeated != mapped || finishCount != 2 {
			t.Fatalf("query finalization not exactly once per changed query: %d %v", finishCount, err)
		}
		if provider.Root.(*plan.ProjectNode).Expressions[0].(plan.ColumnExpr).Scope != analyze.RulePseudoScope {
			t.Fatal("semantic graft mutated stored provider")
		}
	})
	t.Run("persistent_unused_modifier_remap", func(t *testing.T) {
		auxiliary := ruleUnitInsert(e, sink, 7, 70)
		auxiliaryBinding := auxiliary.Binding()
		owned := &plan.CTEDefinition{Statement: auxiliary.Syntax(), Mutation: &auxiliaryBinding, Owner: 1, Name: "ignored"}
		b := original.Binding()
		b.CTEs = []*plan.CTEDefinition{owned}
		mapped, err := stored.RemapRuleBinding(auth.Catalog, b, func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch++; return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if mapped.CTEs[0] == owned || mapped.CTEs[0].Query != nil || mapped.CTEs[0].Statement != auxiliary.Syntax() || cteMutationOf(mapped.CTEs[0]).Ref.Epoch != auxiliary.Binding().Ref.Epoch+1 {
			t.Fatal("unused owned modifier did not retain syntax/nil spool authority and remap target")
		}
		if cteMutationOf(owned).Ref != auxiliary.Binding().Ref {
			t.Fatal("persistent remap mutated frozen modifier")
		}
	})
}

func TestRuleCTEConflictInstantiation(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	original := ruleUnitInsert(e, src, 1, 10).Binding()
	modifier := ruleUnitInsert(e, sink, 1, 10)
	recipe := &analyze.InsertConflict{
		Targets: modifier.Binding().Targets, Keys: []analyze.ConflictKey{{Attribute: 1}},
		Update:        &analyze.UpdateBinding{Assignments: []expr.Expr{ruleUnitInt(sink, 11)}},
		UpdateTargets: []int{1}, Width: 2,
	}
	modifier = modifier.With(func(p *stored.RuleActionParts) { p.Binding.Insert.Conflict = recipe })
	modifierBinding := modifier.Binding()
	definition := &plan.CTEDefinition{Statement: modifier.Syntax(), Mutation: &modifierBinding, Schema: plan.QuerySchema(sink.Columns), Owner: 3}
	scan := &plan.DerivedNode{QuerySchema: definition.Schema, CTE: definition, ScanFrame: 1}
	action := ruleUnitInsert(e, sink, 1, 10)
	action = action.With(func(p *stored.RuleActionParts) {
		p.Binding.Expressions = nil
		p.Binding.Query = &plan.Query{Root: &plan.ProjectNode{
			QuerySchema: plan.QuerySchema(sink.Columns), Frame: 1, Child: scan,
			Expressions: []expr.Expr{ruleUnitInt(sink, 1), ruleUnitInt(sink, 10)},
		}}
		p.Binding.CTEs = []*plan.CTEDefinition{definition}
		p.Binding.Rule.Template = true
	})
	compose := func() analyze.Binding {
		t.Helper()
		result, err := view.ComposeRuleAction(auth, original, "INSERT", stored.Rule{}, action)
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		err = view.NormalizeRuleCTEsLocked(e.catalog.owner(), auth, action.Syntax(), &result)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	executable := func(b analyze.Binding) (*plan.CTEDefinition, *analyze.InsertConflict) {
		t.Helper()
		provider := b.CTEs[0]
		tuple := view.RuleCTEsOf(b.Rule.CTEs)[provider]
		if tuple == nil || tuple.Definition != provider || tuple.Command.Syntax != modifier.Syntax() || tuple.Mutation != -1 {
			t.Fatal("normalization lost the modifier's original provider/executable tuple")
		}
		var found bool
		var visit func(plan.Node)
		visit = func(node plan.Node) {
			if node == nil {
				return
			}
			if derived, ok := node.(*plan.DerivedNode); ok && derived.CTE == provider {
				found = true
				if derived.CTE.Owner != provider.Owner || derived.Query != provider.Query {
					t.Fatal("modifier scan forked its provider or owner")
				}
			}
			left, right := plan.QueryInputs(node)
			visit(left)
			visit(right)
		}
		visit(b.Query.Root)
		if !found || provider.Statement != definition.Statement || cteMutationOf(provider).Insert.Conflict != tuple.Command.Binding.Insert.Conflict {
			t.Fatal("composition/normalization disconnected modifier spool identity")
		}
		return provider, tuple.Command.Binding.Insert.Conflict
	}
	first := compose()
	firstDefinition, firstConflict := executable(first)
	// Mutate the actual normalized execution binding, not a stand-alone clone:
	// execution replaces its normalized predicate through the command's state.
	firstState := dml.NewInsertConflictState(firstConflict)
	firstState.Predicate = ruleUnitInt(sink, 12)
	firstState.MarkAffected(ids.RowID(42), 0)
	second := compose()
	secondDefinition, secondConflict := executable(second)
	if firstConflict == secondConflict || firstConflict == recipe || secondConflict == recipe || firstDefinition == definition || secondDefinition == definition {
		t.Fatal("stored modifier commands share a mutable conflict workspace")
	}
	for _, conflict := range []*analyze.InsertConflict{recipe, secondConflict} {
		if conflict.Predicate != nil {
			t.Fatal("normalized modifier mutated its definition or another command")
		}
		if conflict.Update != recipe.Update || &conflict.Keys[0] != &recipe.Keys[0] || &conflict.UpdateTargets[0] != &recipe.UpdateTargets[0] {
			t.Fatal("unchanged analyzed conflict recipes were unnecessarily cloned")
		}
	}
	if firstConflict.Predicate == nil || firstState.Affected != 1 || firstState.AffectedRows[ids.RowID(42)] != 0 || definition.Mutation != &modifierBinding || definition.Owner != 3 || scan.CTE != definition {
		t.Fatal("command cardinality or frozen definition/provider state changed")
	}
	// Executable descendants use the same stored-action refresh authority.
	nested := modifier.Binding()
	nested.CTEs = []*plan.CTEDefinition{definition}
	ancestor := &plan.CTEDefinition{Statement: modifier.Syntax(), Mutation: &nested, Schema: definition.Schema, Owner: 4}
	refreshed, err := view.RefreshRuleActionCTEs(auth, analyze.Binding{CTEs: []*plan.CTEDefinition{ancestor}})
	if err != nil {
		t.Fatal(err)
	}
	outer := refreshed.CTEs[0]
	inner := cteMutationOf(outer).CTEs[0]
	if outer == ancestor || inner == definition || cteMutationOf(outer).Insert.Conflict == recipe || cteMutationOf(inner).Insert.Conflict == recipe || cteMutationOf(outer).Insert.Conflict == cteMutationOf(inner).Insert.Conflict {
		t.Fatal("recursive stored modifier ancestry retained shared workspace")
	}
	if outer.Owner != ancestor.Owner || inner.Owner != definition.Owner || nested.CTEs[0] != definition || ancestor.Mutation != &nested {
		t.Fatal("recursive refresh mutated stored ownership/ancestry")
	}
	// Fresh ordinary bindings do not enter the stored modifier clone path.
	ordinary, err := view.RefreshRuleActionCTEs(auth, modifier.Binding())
	if err != nil || ordinary.Insert.Conflict != recipe {
		t.Fatal("ordinary binding acquired an unnecessary workspace clone")
	}
}

// cteMutationOf recovers a definition's binding for test assertions.
func cteMutationOf(definition *plan.CTEDefinition) *analyze.Binding {
	mutation, _ := analyze.MutationBinding(definition)
	return mutation
}
