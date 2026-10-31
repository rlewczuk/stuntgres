package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
)

func TestRoutineRuleRecipeOriginalAuthority(t *testing.T) {
	e := engine.New()
	query, source := enginetest.QueryFixture(t, e, "SELECT abs(id) FROM t")
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
	ref := e.Catalog.Domain.Ref(state.Epoch(), captured.Address())
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
		Mode:                  catalog.RuleOrigin,
		ConditionPresentation: condition,
		Actions:               []stored.RuleAction{stored.NewRuleAction(stored.RuleActionParts{Presentation: presentation})},
	})
	owner.Rules = stored.RulesPayload([]stored.Rule{rule})
	state.SetDescriptor(owner.Address(), &owner)
	auth := enginetest.CatalogAuthorization(e, e.Catalog.Read(state))
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
			private := j.PrivateRead(&e.Catalog.Owner, committed)
			authority := enginetest.CatalogAuthorization(e, private)
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
				foreign := engine.New().CatalogRead()
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
				checks[0].Ref = engine.New().Catalog.Domain.Ref(state.Epoch(), captured.Address())
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
			authority := enginetest.CatalogAuthorization(e, e.Catalog.Read(candidate))
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
