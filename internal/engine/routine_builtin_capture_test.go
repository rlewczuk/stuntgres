package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
)

// The public regression proves real view/body execution and ACL transitions.
// This unit checks the otherwise unobservable proof boundary: a shared alias
// must retain its OWN historical declaration, never the primary/current one.
func TestRoutineBuiltinCapturedDeclarations(t *testing.T) {
	for _, query := range []string{
		"SELECT bool_and(f),every(f),bool_and(f) FROM t",
		"SELECT row_number() OVER(ORDER BY id) FROM t",
		"SELECT DISTINCT id FROM t",
		"SELECT id FROM t GROUP BY id",
		"SELECT sum(v) OVER(PARTITION BY id) FROM t",
		"SELECT rank() OVER(ORDER BY id) FROM t",
	} {
		t.Run(query, func(t *testing.T) {
			e, bound, source := queryCoreFixture(t, query)
			defer source.Close()
			var captures []plan.AggregateRoutineAlias
			plan.VisitExecutableQuery(bound.Root, func(node plan.Node) {
				switch n := node.(type) {
				case *plan.AggregateNode:
					if query == "SELECT bool_and(f),every(f),bool_and(f) FROM t" && (len(n.Calls) != 1 || len(n.Calls[0].RoutineAliases) != 1) {
						t.Fatal("shared computation no longer retains one independent alias", n.Calls)
					}
					for _, group := range n.Grouping {
						captures = append(captures, plan.AggregateRoutineAlias{Ref: group.Routine, Procedure: group.Procedure})
					}
					for _, call := range n.Calls {
						captures = append(captures, plan.AggregateRoutineAlias{Ref: call.Routine, Procedure: call.Procedure})
						captures = append(captures, call.RoutineAliases...)
					}
				case *plan.WindowNode:
					for _, call := range n.Calls {
						captures = append(captures, plan.AggregateRoutineAlias{Ref: call.Routine, Procedure: call.Procedure})
					}
					for _, spec := range n.Specs {
						for _, group := range spec.Grouping {
							captures = append(captures, plan.AggregateRoutineAlias{Ref: group.Routine, Procedure: group.Procedure})
						}
						for _, key := range spec.Order {
							captures = append(captures, plan.AggregateRoutineAlias{Ref: key.EqualityRoutine, Procedure: key.EqualityProcedure})
						}
					}
				case *plan.DistinctNode:
					for _, group := range n.Grouping {
						captures = append(captures, plan.AggregateRoutineAlias{Ref: group.Routine, Procedure: group.Procedure})
					}
				}
			}, func(expr.Expr) {})
			if len(captures) == 0 {
				t.Fatal("no captured declarations")
			}
			base := e.CatalogRead()
			state := catalog.CloneState(base.State())
			var dependencies []catalog.ExpressionDependency
			for _, capture := range captures {
				p := capture.Procedure
				if p == nil || p.Builtin == nil || p.Address() != capture.Ref.Address || p != base.MustProcedure(capture.Ref.Address) {
					t.Fatal("producer did not retain its original checked declaration", capture)
				}
				dependencies = append(dependencies, catalog.ExpressionDependency{Ref: capture.Ref, Revision: p.Revision()})
				current := *p
				current.ObjectName = "captured_" + p.Name()
				current.ObjectRevision++
				state.UnbindName(catalog.DescriptorNameKey(p))
				state.SetDescriptor(current.Address(), &current)
				state.BindName(catalog.DescriptorNameKey(&current), current.Address())
				index := catalog.ProcedureIndexEdit{Index: state.ProcedureNames()}
				index.Replace(p, &current)
				state.SetProcedureNames(index.Index)
			}
			auth := catalogAuthorization(e, e.catalog.Read(state))
			context := exec.Context{EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: dependencies}}
			for _, capture := range captures {
				if err := exec.AdmitRoutineReference(&context, capture.Ref, capture.Procedure, "function"); err != nil {
					t.Fatal("original checked occurrence rejected", err)
				}
				if err := exec.AdmitRoutineReference(&context, capture.Ref, nil, "function"); err == nil {
					t.Fatal("reference-only occurrence acquired historical declaration proof")
				}
				current := state.MustProcedure(capture.Ref.Address)
				if err := exec.AdmitRoutineReference(&context, capture.Ref, current, "function"); err == nil {
					t.Fatal("current descriptor beside historical manifest accepted")
				}
				if err := exec.AdmitRoutineDescriptor(auth.AdmissionAccess(), dependencies, capture.Procedure, nil); err == nil {
					t.Fatal("direct source lost revision fence")
				}
				for _, other := range captures {
					if other.Ref != capture.Ref && other.Ref != (ids.ObjectRef{}) {
						if err := exec.AdmitRoutineReference(&context, capture.Ref, other.Procedure, "function"); err == nil {
							t.Fatal("primary/alias declarations interchangeable")
						}
					}
				}
			}
		})
	}
}

func TestRoutineRuleRecipeOriginalAuthority(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT abs(id) FROM t")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Expressions[0]
	captured, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || captured == nil {
		t.Fatal("original selected builtin", err)
	}
	base := e.CatalogRead()
	state := catalog.CloneState(base.State())
	current := *captured
	current.ObjectName, current.ObjectRevision = "rule_abs", captured.Revision()+1
	state.UnbindName(catalog.DescriptorNameKey(captured))
	state.SetDescriptor(current.Address(), &current)
	state.BindName(catalog.DescriptorNameKey(&current), current.Address())
	index := catalog.ProcedureIndexEdit{Index: state.ProcedureNames()}
	index.Replace(captured, &current)
	state.SetProcedureNames(index.Index)
	ref := e.catalog.Domain.Ref(state.Epoch(), captured.Address())
	dependencies := []catalog.ExpressionDependency{{Ref: ref, Revision: captured.Revision()}}
	presentation := stored.NewView(stored.ViewParts{Dependencies: dependencies})
	condition := stored.NewView(stored.ViewParts{Dependencies: dependencies})
	table, ok := base.Relation(source.Ref.Address)
	if !ok {
		t.Fatal("actual source container")
	}
	owner := *table
	rule := stored.NewRule(stored.RuleParts{
		OID: 900101, Name: "original_rule", Event: "INSERT",
		ConditionPresentation: condition,
		Actions:               []stored.RuleAction{stored.NewRuleAction(stored.RuleActionParts{Presentation: presentation})},
	})
	owner.Rules = stored.RulesPayload([]stored.Rule{rule})
	state.SetDescriptor(owner.Address(), &owner)
	auth := catalogAuthorization(e, e.catalog.Read(state))
	recipe := &catalog.RuleRoutineRecipe{Owner: &owner, Rule: rule.OID, Action: 0, Presentation: presentation}
	evaluation := exec.Context{EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	if err := exec.SelectExpressionRecipe(&evaluation, recipe); err != nil {
		t.Fatal("genuine original rule slot", err)
	}
	if err := exec.AdmitRoutineReference(&evaluation, ref, captured, "function"); err != nil {
		t.Fatal("original rule manifest rejected after COW", err)
	}
	if err := exec.AdmitRoutineReference(&evaluation, ref, &current, "function"); err == nil {
		t.Fatal("current descriptor substituted beside historical manifest")
	}
	if err := exec.AdmitRoutineDescriptor(auth.AdmissionAccess(), dependencies, captured, nil); err == nil {
		t.Fatal("fresh producer inherited rule lifetime")
	}
	conditionRecipe := *recipe
	conditionRecipe.Action, conditionRecipe.Presentation = -1, condition
	conditionContext := exec.Context{EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	if err := exec.SelectExpressionRecipe(&conditionContext, &conditionRecipe); err != nil {
		t.Fatal("genuine original condition slot", err)
	}
	if err := exec.AdmitRoutineReference(&conditionContext, ref, captured, "function"); err != nil {
		t.Fatal("condition lost original declaration lifetime", err)
	}
	t.Run("private-current-slot-and-savepoint-authority", func(t *testing.T) {
		committed := catalog.CloneState(state)
		committed.SetDescriptor(table.Address(), table)
		before := session.NewJournal()
		created := before.Clone()
		created.RecordDescriptor(committed, owner.Address(), &owner)
		check := func(j *session.Journal, probe *catalog.RuleRoutineRecipe, want bool) {
			t.Helper()
			private := j.PrivateRead(&e.catalog.Owner, committed)
			authority := catalogAuthorization(e, private)
			local := exec.Context{EvalAdmission: exec.EvalAdmission{RoutineAuthority: &authority}}
			err := exec.SelectExpressionRecipe(&local, probe)
			if !want {
				if err == nil {
					t.Fatal("obsolete private slot borrowed current authority")
				}
				return
			}
			if err != nil {
				t.Fatal("genuine private slot rejected", err)
			}
			if err := exec.AdmitRoutineReference(&local, ref, captured, "function"); err != nil {
				t.Fatal("private original declaration rejected", err)
			}
			if err := exec.AdmitRoutineDescriptor(authority.AdmissionAccess(), dependencies, captured, nil); err == nil {
				t.Fatal("fresh producer borrowed private recipe authority")
			}
		}
		check(created, recipe, true)
		check(before, recipe, false) // rolled-back CREATE is not live authority
		renamed := owner
		renamedRule := rule.With(func(p *stored.RuleParts) { p.Name = "renamed_private" })
		renamed.Rules = stored.RulesPayload([]stored.Rule{renamedRule})
		renaming := created.Clone()
		renaming.RecordDescriptor(committed, owner.Address(), &renamed)
		renamedRecipe := *recipe
		renamedRecipe.Owner = &renamed
		check(renaming, &renamedRecipe, true)
		replacement := presentation.With(func(p *stored.ViewParts) {
			p.Dependencies = append([]catalog.ExpressionDependency(nil), dependencies...)
		})
		replaced := owner
		replaced.Rules = stored.RulesPayload([]stored.Rule{rule.With(func(p *stored.RuleParts) {
			p.Actions = []stored.RuleAction{stored.NewRuleAction(stored.RuleActionParts{Presentation: replacement})}
		})})
		replacing := created.Clone()
		replacing.RecordDescriptor(committed, owner.Address(), &replaced)
		newRecipe := *recipe
		newRecipe.Owner = &replaced
		newRecipe.Presentation = replacement
		check(replacing, &newRecipe, true)
		stale := *recipe
		stale.Owner = &replaced
		check(replacing, &stale, false)
		check(created, &newRecipe, false) // rollback-to restores the old exact slot
		check(created, recipe, true)
		removed := owner
		removed.Rules = nil
		dropping := created.Clone()
		dropping.RecordDescriptor(committed, owner.Address(), &removed)
		check(dropping, recipe, false)
	})
	standalone := exec.Context{}
	if err := exec.SelectExpressionRecipe(&standalone, recipe); err == nil {
		t.Fatal("rule manifest borrowed without actual live container authority")
	}
	for _, mutation := range []string{"rule", "slot", "presentation", "foreign_owner", "temporary_shape"} {
		t.Run(mutation, func(t *testing.T) {
			probe := *recipe
			switch mutation {
			case "rule":
				probe.Rule++
			case "slot":
				probe.Action++
			case "presentation":
				probe.Presentation = condition
			case "foreign_owner":
				foreign := New().CatalogRead()
				copy := owner
				copy.Owner = foreign.Owner().Principals.Principal(owner.Owner.ID())
				probe.Owner = &copy
			case "temporary_shape":
				probe.Presentation = presentation.With(func(p *stored.ViewParts) { p.Consumer = captured.Address() })
			}
			local := exec.Context{EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
			if err := exec.SelectExpressionRecipe(&local, &probe); err == nil {
				t.Fatal("invalid rule occurrence borrowed original authority")
			}
		})
	}
	for _, mutation := range []string{"revision", "epoch", "foreign", "missing"} {
		t.Run(mutation, func(t *testing.T) {
			checks := append([]catalog.ExpressionDependency(nil), dependencies...)
			switch mutation {
			case "revision":
				checks[0].Revision = current.Revision()
			case "epoch":
				checks[0].Ref.Epoch++
			case "foreign":
				checks[0].Ref = New().catalog.Domain.Ref(state.Epoch(), captured.Address())
			case "missing":
				checks = nil
			}
			payload := stored.NewView(stored.ViewParts{Dependencies: checks})
			action := stored.NewRuleAction(stored.RuleActionParts{Presentation: payload})
			changedRule := rule.With(func(p *stored.RuleParts) { p.Actions = []stored.RuleAction{action} })
			changedOwner := owner
			changedOwner.Rules = stored.RulesPayload([]stored.Rule{changedRule})
			candidate := catalog.CloneState(state)
			candidate.SetDescriptor(changedOwner.Address(), &changedOwner)
			authority := catalogAuthorization(e, e.catalog.Read(candidate))
			probe := &catalog.RuleRoutineRecipe{Owner: &changedOwner, Rule: rule.OID, Action: 0, Presentation: payload}
			local := exec.Context{EvalAdmission: exec.EvalAdmission{RoutineAuthority: &authority}}
			if err := exec.SelectExpressionRecipe(&local, probe); err != nil {
				t.Fatal("real slot must not mask the manifest diagnostic", err)
			}
			if err := exec.AdmitRoutineReference(&local, ref, captured, "function"); err == nil {
				t.Fatal("invalid original manifest was healed")
			}
		})
	}
}
