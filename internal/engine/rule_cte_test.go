package engine

import (
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRuleCTECommandNormalization(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	t.Run("expanded_provenance", func(t *testing.T) {
		for _, test := range []struct {
			name     string
			commands []ruleCommand
			message  string
		}{
			{"single_select", []ruleCommand{{syntax: &parser.Select{}, source: ruleInstead}}, ""},
			{"single_original", []ruleCommand{{source: ruleOriginal}}, ""},
			{"nothing", nil, "DO INSTEAD NOTHING"},
			{"conditional", []ruleCommand{{source: ruleConditionalInstead}, {source: ruleInstead}}, "conditional DO INSTEAD"},
			{"recursive_also", []ruleCommand{{source: ruleInstead}, {source: ruleAlso}}, "DO ALSO"},
			{"recursive_multi", []ruleCommand{{source: ruleInstead}, {source: ruleInstead}}, "multi-statement DO INSTEAD"},
		} {
			t.Run(test.name, func(t *testing.T) {
				err := ruleCTEProgramError(ruleProgram{commands: test.commands})
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
	definition := &cteDefinition{statement: original.syntax, mutation: &original.binding, schema: querySchema(src.Columns), owner: 1}
	t.Run("complete_executable_tuple", func(t *testing.T) {
		action := ruleUnitInsert(e, sink, 1, 11)
		src.Rules = rulesPayloadOf([]ruleDefinition{{Name: "redirect", event: "INSERT", instead: true, actions: []ruleAction{action}}})
		defer func() { src.Rules = nil }()
		parent := binding{ctes: []*cteDefinition{definition}}
		e.mu.Lock()
		err := e.normalizeRuleCTEsLocked(nil, auth, &parser.Select{}, &parent)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		rewrite := parent.rule.ctes[definition]
		if rewrite == nil || rewrite.definition != definition || rewrite.definition.statement != original.syntax || rewrite.definition.mutation != &original.binding || rewrite.command.syntax != action.syntax || rewrite.command.binding.ref != action.binding.ref || rewrite.mutation != -1 {
			t.Fatal("normalization did not preserve the provider and select the executable target")
		}
		if definition.query != nil {
			t.Fatal("modifier acquired a SELECT provider")
		}
	})
	t.Run("protected_modifier_and_select", func(t *testing.T) {
		provider := &BoundQuery{Root: &projectNode{querySchema: querySchema(src.Columns), frame: 2, child: &singletonNode{}, expressions: []expr.Expr{ruleUnitInt(src, 1), ruleUnitInt(src, 10)}}}
		selection := &cteDefinition{query: provider, owner: 1}
		for _, protected := range []*cteDefinition{definition, selection} {
			scan := &derivedNode{querySchema: querySchema(src.Columns), query: protected.query, cte: protected, scanFrame: 1}
			mapped, err := newRuleTreeMapper(auth.Catalog, func(frame uint32) uint32 { return frame + 7 }, 4, []*cteDefinition{protected}).query(scan)
			if err != nil {
				t.Fatal(err)
			}
			result := mapped.(*derivedNode)
			if result.cte != protected || result.query != protected.query || result.cte.owner != 1 || result.scanFrame != 8 {
				t.Fatal("protected producer identity/owner/query graph changed")
			}
		}
	})
	t.Run("action_owner_and_project_memo", func(t *testing.T) {
		leaf := &sourceNode{querySchema: querySchema(sink.Columns), occurrence: 1}
		provider := &BoundQuery{Root: &projectNode{querySchema: querySchema(sink.Columns), frame: 2, child: leaf, expressions: []expr.Expr{ruleUnitInt(sink, 1), ruleUnitInt(sink, 11)}}}
		local := &cteDefinition{query: provider, owner: 1}
		scan := &derivedNode{querySchema: querySchema(sink.Columns), query: provider, cte: local, scanFrame: 1}
		action := binding{ctes: []*cteDefinition{local}, query: &BoundQuery{Root: &projectNode{querySchema: querySchema(sink.Columns), frame: 1, child: scan, expressions: provider.Root.(*projectNode).expressions, analysisExpressions: []expr.Expr{subqueryExpression{queryPlan: provider, cteDefinition: true}}}}}
		parent := original.binding
		parent.ctes = []*cteDefinition{definition}
		mapped, err := isolateRuleCTEFrames(auth.Catalog, parent, action, &parser.Insert{})
		if err != nil {
			t.Fatal(err)
		}
		mapped, err = offsetRuleSources(auth.Catalog, mapped, 3, parent.ctes)
		if err != nil {
			t.Fatal(err)
		}
		project := mapped.query.Root.(*projectNode)
		result := project.child.(*derivedNode)
		analysis := project.analysisExpressions[0].(subqueryExpression)
		if result.cte == local || result.cte.owner == definition.owner || result.scanFrame != 1 || result.query != result.cte.query || mapped.ctes[0] != result.cte || analysis.queryPlan.Root != result.cte.query.Root {
			t.Fatal("action provider mapping is incoherent or shadows protected owner")
		}
		if result.query.Root.(*projectNode).frame == 2 || result.query.Root.(*projectNode).child.(*sourceNode).occurrence != 4 {
			t.Fatal("provider frame/occurrence was not mapped exactly once")
		}
		final, err := finalizeRuleQuery(auth.Catalog, project)
		if err != nil {
			t.Fatal(err)
		}
		finalScan := final.(*projectNode).child.(*derivedNode)
		if finalScan.cte != result.cte || finalScan.query != result.cte.query {
			t.Fatal("finalization forked immutable provider graph")
		}
		if local.owner != 1 || provider.Root.(*projectNode).frame != 2 || leaf.occurrence != 1 {
			t.Fatal("mutated frozen action template")
		}
	})
	t.Run("top_owned_namespace_only", func(t *testing.T) {
		position := parser.Position{Offset: 17}
		raw := parser.CommonTableExpression{Name: parser.Identifier{Text: "q", Pos: position}}
		newColumn := columnExpression{Info: ruleColumnInfo(src.Columns[0]), scope: rulePseudoScope, frame: 2}
		oldColumn := newColumn
		oldColumn.frame = 1
		provider := &BoundQuery{Root: &projectNode{expressions: []expr.Expr{newColumn, oldColumn}}}
		local := &cteDefinition{query: provider, position: position}
		body := binding{ctes: []*cteDefinition{local}, query: &BoundQuery{Root: &projectNode{analysisExpressions: []expr.Expr{subqueryExpression{Info: expr.Info{Pos: position}, queryPlan: provider, cteDefinition: true}}}}}
		for _, action := range []parser.Statement{
			&parser.Insert{With: []parser.CommonTableExpression{raw}},
			&parser.Insert{Source: &parser.InsertSource{Select: &parser.Select{With: []parser.CommonTableExpression{raw}}}},
		} {
			err := validateRuleActionCTEs(action, body)
			if err == nil || !strings.Contains(err.Error(), "cannot refer to OLD within WITH query") {
				t.Fatalf("OLD priority/owned scope: %v", err)
			}
		}
		if err := validateRuleActionCTEs(&parser.Insert{}, body); err != nil {
			t.Fatalf("nested/enclosing WITH was rejected: %v", err)
		}
		provider.Root = &projectNode{expressions: []expr.Expr{rowExpression{ruleSide: 2}}}
		if err := validateRuleActionCTEs(&parser.Insert{With: []parser.CommonTableExpression{raw}}, body); err == nil || !strings.Contains(err.Error(), "cannot refer to NEW within WITH query") {
			t.Fatalf("empty formal whole row lost identity: %v", err)
		}
	})
	t.Run("semantic_provider_memo_and_finalization", func(t *testing.T) {
		pseudo := columnExpression{Info: ruleColumnInfo(src.Columns[0]), scope: rulePseudoScope, frame: 2}
		provider := &BoundQuery{Root: &projectNode{querySchema: querySchema(sink.Columns), frame: 2, child: &singletonNode{}, expressions: []expr.Expr{pseudo, ruleUnitInt(sink, 10)}}}
		local := &cteDefinition{query: provider, owner: 1}
		scan := &derivedNode{querySchema: querySchema(sink.Columns), cte: local, query: provider, scanFrame: 1}
		root := &projectNode{querySchema: querySchema(sink.Columns), frame: 1, child: scan, expressions: []expr.Expr{ruleUnitInt(sink, 1), ruleUnitInt(sink, 10)}, analysisExpressions: []expr.Expr{subqueryExpression{queryPlan: provider, cteDefinition: true}}}
		finalized := make(map[LogicalNode]bool)
		finishCount := 0
		mapper := newRuleTreeMapper(auth.Catalog, nil, 0, nil, ruleTreeMapping{
			leaf: func(expr expr.Expr) (expr.Expr, bool, error) {
				if column, ok := expr.(columnExpression); ok && column.scope == rulePseudoScope {
					return ruleUnitInt(sink, 11), true, nil
				}
				return expr, false, nil
			},
			finish: func(root LogicalNode) (LogicalNode, error) {
				finishCount++
				result, err := finalizeRuleQuery(auth.Catalog, root, finalized)
				if err == nil {
					finalized[result] = true
				}
				return result, err
			},
		})
		mapped, err := mapper.query(root)
		if err != nil {
			t.Fatal(err)
		}
		project := mapped.(*projectNode)
		result := project.child.(*derivedNode)
		analysis := project.analysisExpressions[0].(subqueryExpression)
		definition, err := mapper.definition(local)
		if err != nil {
			t.Fatal(err)
		}
		if definition != result.cte || result.query != definition.query || analysis.queryPlan.Root != definition.query.Root {
			t.Fatal("semantic graft forked definition/scan/analysis project")
		}
		if _, formal := definition.query.Root.(*projectNode).expressions[0].(columnExpression); formal {
			t.Fatal("formal reference survived semantic provider mapping")
		}
		repeated, err := mapper.query(root)
		if err != nil || repeated != mapped || finishCount != 2 {
			t.Fatalf("query finalization not exactly once per changed query: %d %v", finishCount, err)
		}
		if provider.Root.(*projectNode).expressions[0].(columnExpression).scope != rulePseudoScope {
			t.Fatal("semantic graft mutated stored provider")
		}
	})
	t.Run("persistent_unused_modifier_remap", func(t *testing.T) {
		auxiliary := ruleUnitInsert(e, sink, 7, 70)
		owned := &cteDefinition{statement: auxiliary.syntax, mutation: &auxiliary.binding, owner: 1, name: "ignored"}
		b := original.binding
		b.ctes = []*cteDefinition{owned}
		mapped, err := remapRuleBinding(auth.Catalog, b, func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch++; return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil)
		if err != nil {
			t.Fatal(err)
		}
		if mapped.ctes[0] == owned || mapped.ctes[0].query != nil || mapped.ctes[0].statement != auxiliary.syntax || cteMutationOf(mapped.ctes[0]).ref.Epoch != auxiliary.binding.ref.Epoch+1 {
			t.Fatal("unused owned modifier did not retain syntax/nil spool authority and remap target")
		}
		if cteMutationOf(owned).ref != auxiliary.binding.ref {
			t.Fatal("persistent remap mutated frozen modifier")
		}
	})
}

func TestRuleCTEConflictInstantiation(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	original := ruleUnitInsert(e, src, 1, 10).binding
	modifier := ruleUnitInsert(e, sink, 1, 10)
	recipe := &boundInsertConflict{
		targets: modifier.binding.targets, keys: []conflictKey{{attribute: 1}},
		update:        &boundUpdate{assignments: []expr.Expr{ruleUnitInt(sink, 11)}},
		updateTargets: []int{1}, width: 2,
	}
	modifier.binding.insert.conflict = recipe
	definition := &cteDefinition{statement: modifier.syntax, mutation: &modifier.binding, schema: querySchema(sink.Columns), owner: 3}
	scan := &derivedNode{querySchema: definition.schema, cte: definition, scanFrame: 1}
	action := ruleUnitInsert(e, sink, 1, 10)
	action.binding.expressions = nil
	action.binding.query = &BoundQuery{Root: &projectNode{
		querySchema: querySchema(sink.Columns), frame: 1, child: scan,
		expressions: []expr.Expr{ruleUnitInt(sink, 1), ruleUnitInt(sink, 10)},
	}}
	action.binding.ctes = []*cteDefinition{definition}
	action.binding.rule.template = true
	compose := func() binding {
		t.Helper()
		result, err := composeRuleAction(auth, original, "INSERT", ruleDefinition{}, action)
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		err = e.normalizeRuleCTEsLocked(nil, auth, action.syntax, &result)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	executable := func(b binding) (*cteDefinition, *boundInsertConflict) {
		t.Helper()
		provider := b.ctes[0]
		tuple := b.rule.ctes[provider]
		if tuple == nil || tuple.definition != provider || tuple.command.syntax != modifier.syntax || tuple.mutation != -1 {
			t.Fatal("normalization lost the modifier's original provider/executable tuple")
		}
		var found bool
		var visit func(LogicalNode)
		visit = func(node LogicalNode) {
			if node == nil {
				return
			}
			if derived, ok := node.(*derivedNode); ok && derived.cte == provider {
				found = true
				if derived.cte.owner != provider.owner || derived.query != provider.query {
					t.Fatal("modifier scan forked its provider or owner")
				}
			}
			left, right := queryInputs(node)
			visit(left)
			visit(right)
		}
		visit(b.query.Root)
		if !found || provider.statement != definition.statement || cteMutationOf(provider).insert.conflict != tuple.command.binding.insert.conflict {
			t.Fatal("composition/normalization disconnected modifier spool identity")
		}
		return provider, tuple.command.binding.insert.conflict
	}
	first := compose()
	firstDefinition, firstConflict := executable(first)
	// Mutate the actual normalized execution binding, not a stand-alone clone.
	firstConflict.markAffected(ids.RowID(42), 0)
	firstConflict.selected = []ids.ObjectAddress{sink.Address()}
	firstConflict.catalog = auth.Catalog.State()
	firstConflict.checkpoint = make(lock.Checkpoint)
	firstConflict.statementKeys = make(lock.Checkpoint)
	firstConflict.frame = make([]sqltype.Value, 4)
	firstConflict.overlays = make(map[*uniqueBoundKey]*insertWriteKeyOverlay)
	firstConflict.ownRowSlots = map[ids.ObjectAddress]map[ids.RowID]int{sink.Address(): {ids.RowID(42): 0}}
	second := compose()
	secondDefinition, secondConflict := executable(second)
	if firstConflict == secondConflict || firstConflict == recipe || secondConflict == recipe || firstDefinition == definition || secondDefinition == definition {
		t.Fatal("stored modifier commands share a mutable conflict workspace")
	}
	for _, conflict := range []*boundInsertConflict{recipe, secondConflict} {
		if conflict.affected != 0 || conflict.affectedRows != nil || conflict.selected != nil || conflict.catalog != nil || conflict.checkpoint != nil || conflict.statementKeys != nil || conflict.frame != nil || conflict.overlays != nil || conflict.ownRowSlots != nil {
			t.Fatal("normalized modifier mutated its definition or another command")
		}
		if conflict.update != recipe.update || &conflict.keys[0] != &recipe.keys[0] || &conflict.updateTargets[0] != &recipe.updateTargets[0] {
			t.Fatal("unchanged analyzed conflict recipes were unnecessarily cloned")
		}
	}
	if firstConflict.affected != 1 || firstConflict.affectedRows[ids.RowID(42)] != 0 || definition.mutation != &modifier.binding || definition.owner != 3 || scan.cte != definition {
		t.Fatal("command cardinality or frozen definition/provider state changed")
	}
	// Executable descendants use the same stored-action refresh authority.
	nested := modifier.binding
	nested.ctes = []*cteDefinition{definition}
	ancestor := &cteDefinition{statement: modifier.syntax, mutation: &nested, schema: definition.schema, owner: 4}
	refreshed, err := refreshRuleActionCTEs(auth, binding{ctes: []*cteDefinition{ancestor}})
	if err != nil {
		t.Fatal(err)
	}
	outer := refreshed.ctes[0]
	inner := cteMutationOf(outer).ctes[0]
	if outer == ancestor || inner == definition || cteMutationOf(outer).insert.conflict == recipe || cteMutationOf(inner).insert.conflict == recipe || cteMutationOf(outer).insert.conflict == cteMutationOf(inner).insert.conflict {
		t.Fatal("recursive stored modifier ancestry retained shared workspace")
	}
	if outer.owner != ancestor.owner || inner.owner != definition.owner || nested.ctes[0] != definition || ancestor.mutation != &nested {
		t.Fatal("recursive refresh mutated stored ownership/ancestry")
	}
	// Fresh ordinary bindings do not enter the stored modifier clone path.
	ordinary, err := refreshRuleActionCTEs(auth, modifier.binding)
	if err != nil || ordinary.insert.conflict != recipe {
		t.Fatal("ordinary binding acquired an unnecessary workspace clone")
	}
}

// cteMutationOf recovers a definition's binding for test assertions.
func cteMutationOf(definition *cteDefinition) *binding {
	mutation, _ := mutationBinding(definition)
	return mutation
}
