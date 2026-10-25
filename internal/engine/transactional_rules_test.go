package engine

import (
	"context"
	"errors"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"testing"
)

// Lock scheduling may serialize the public retirement witness. These pure
// roots prove that commit never admits a late ordinary-rule edge, and that
// candidate rewiring/removal, rather than the obsolete committed owner, wins.
func TestTransactionalRulesDependencyPreflight(t *testing.T) {
	for _, kind := range []string{"target", "column", "nested-target", "constraint"} {
		for _, control := range []string{"dependent", "replace-away", "drop-rule", "drop-owner", "unrelated"} {
			t.Run(kind+"/"+control, func(t *testing.T) {
				e, auth, src, sink := ruleUnitCatalog(t)
				base := auth.Catalog.State()
				action := ruleUnitInsert(e, sink, 1, 10)
				target := sink.Address()
				switch kind {
				case "column":
					target.SubID = 2
				case "nested-target":
					inner := action.Binding()
					action = ruleUnitInsert(e, src, 1, 10).With(func(p *stored.RuleActionParts) {
						p.Binding.CTEs = []*plan.CTEDefinition{{Statement: action.Syntax(), Mutation: &inner, Owner: 1}}
					})
				case "constraint":
					target = ids.ObjectAddress{ClassID: 2606, ObjectID: 900202}
					action = action.With(func(p *stored.RuleActionParts) {
						p.Binding.Insert.Conflict = &analyze.InsertConflict{Constraint: target}
					})
				}
				rule := stored.NewRule(stored.RuleParts{OID: 900101, Name: "late", Event: "INSERT", Actions: []stored.RuleAction{action}})
				refs, err := view.RuleReferences(context.Background(), auth.Catalog, rule)
				if err != nil {
					t.Fatal(err)
				}
				endpoint := target
				if kind == "nested-target" {
					endpoint.SubID = 2
				}
				if kind != "target" && !slices.Contains(refs, endpoint) {
					t.Fatal("typed endpoint missing", endpoint, refs)
				}
				if !view.RuleDepends(auth.Catalog, rule.With(func(p *stored.RuleParts) { p.References = refs }), target) {
					t.Fatal("retired provider dependency missing", target, refs)
				}
				rule = rule.With(func(p *stored.RuleParts) { p.References = refs })
				owner := *src
				owner.ObjectRevision++
				owner.Rules = stored.RulesPayload([]stored.Rule{rule})
				current := catalog.CloneState(base)
				current.SetDescriptor(owner.Address(), &owner)
				candidate := catalog.CloneState(current)
				// The retired target need not be absent for column/constraint retirement;
				// the observed address itself is the dependency invalidation frontier.
				if kind == "target" || kind == "nested-target" {
					candidate.SetDescriptor(sink.Address(), nil)
				}
				switch control {
				case "replace-away":
					replacement := ruleUnitInsert(e, src, 1, 10)
					rewired := rule.With(func(p *stored.RuleParts) {
						p.Actions = []stored.RuleAction{replacement}
						p.References = []ids.ObjectAddress{src.Address()}
					})
					changed := owner
					changed.Rules = stored.RulesPayload([]stored.Rule{rewired})
					candidate.SetDescriptor(owner.Address(), &changed)
				case "drop-rule":
					changed := owner
					changed.Rules = nil
					candidate.SetDescriptor(owner.Address(), &changed)
				case "drop-owner":
					candidate.SetDescriptor(owner.Address(), nil)
				case "unrelated":
					current = catalog.CloneState(base)
					other := *src
					other.ObjectRevision++
					other.ObjectName = "peer_name"
					current.SetDescriptor(src.Address(), &other)
					candidate = catalog.CloneState(current)
					candidate.SetDescriptor(sink.Address(), nil)
				}
				err = CatalogDependencyPreflight(current, candidate, []session.DependencyObservation{{Base: base, Targets: []ids.ObjectAddress{target}}}, nil)
				var conflict catalog.Conflict
				if control == "dependent" {
					if !errors.As(err, &conflict) {
						t.Fatalf("late %s edge admitted: %v", kind, err)
					}
				} else if err != nil {
					t.Fatalf("obsolete/unrelated edge rejected: %v", err)
				}
				if base.Get(src.Address()) != src || stored.RulesOf(src) != nil {
					t.Fatal("preflight mutated captured base")
				}
				if control == "unrelated" && candidate.Get(src.Address()) != current.Get(src.Address()) {
					t.Fatal("unrelated peer descriptor lost")
				}
			})
		}
	}
}

// The typed closure must retain mutation targets even when they have no FROM
// occurrence, and query/qualification providers independently of those targets.
func TestTransactionalRulesActionAdmission(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	provider := *sink
	provider.ObjectAddress.ObjectID = 900003
	provider.ObjectName = "provider"
	state := catalog.CloneState(auth.Catalog.State())
	state.SetDescriptor(provider.Address(), &provider)
	read := catalog.NewRead(auth.Catalog.Owner(), state)
	outer := ruleUnitInsert(e, src, 1, 10)
	nested := ruleUnitInsert(e, sink, 2, 20)
	binding := nested.Binding()
	providerRef := e.Catalog.Domain.Ref(read.Epoch(), provider.Address())
	outer = outer.With(func(p *stored.RuleActionParts) {
		p.Binding.CTEs = []*plan.CTEDefinition{{Statement: nested.Syntax(), Mutation: &binding, Owner: 1}}
		p.Binding.Query = &plan.Query{Root: &plan.SourceNode{QuerySchema: plan.QuerySchema(provider.Columns), Ref: providerRef}}
	})
	conditionRef := ruleUnitRef(e, src)
	conditionRef.Address.SubID = 2
	rule := stored.NewRule(stored.RuleParts{Actions: []stored.RuleAction{outer}, Condition: plan.ColumnExpr{Info: view.RuleColumnInfo(src.Columns[1]), Origin: conditionRef}})
	refs, err := view.RuleReferences(context.Background(), read, rule)
	if err != nil {
		t.Fatal(err)
	}
	rule = rule.With(func(p *stored.RuleParts) { p.References = refs })
	for _, address := range []ids.ObjectAddress{src.Address(), sink.Address(), provider.Address(), conditionRef.Address} {
		if !view.RuleDepends(read, rule, address) {
			t.Fatal("missing action/qualification provider", address, refs)
		}
	}
	absent := provider.Address()
	absent.ObjectID++
	if view.RuleDepends(read, rule, absent) {
		t.Fatal("fabricated provider", absent)
	}
}
