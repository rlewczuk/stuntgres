package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// Exercise the real query admission and rule-slot protocol. Generic dependency
// membership must not turn a fresh key into an original stored occurrence.
func TestRoutineEqualityRecipeAuthority(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT DISTINCT id FROM t")
	defer source.Close()
	var distinct *plan.DistinctNode
	plan.VisitExecutableQuery(query.Root, func(node plan.Node) {
		if n, ok := node.(*plan.DistinctNode); ok {
			distinct = n
		}
	}, func(expr.Expr) {})
	if distinct == nil || len(distinct.Grouping) != 1 || distinct.Grouping[0].Procedure == nil {
		t.Fatal("missing original checked grouping")
	}
	group := distinct.Grouping[0]
	original := group.Procedure
	base := e.CatalogRead()
	state := catalog.CloneState(base.State())
	current := *original
	current.ObjectName, current.ObjectRevision = "rule_equality", original.Revision()+1
	state.UnbindName(catalog.DescriptorNameKey(original))
	state.SetDescriptor(current.Address(), &current)
	state.BindName(catalog.DescriptorNameKey(&current), current.Address())
	index := catalog.ProcedureIndexEdit{Index: state.ProcedureNames()}
	index.Replace(original, &current)
	state.SetProcedureNames(index.Index)
	dependencies := []catalog.ExpressionDependency{{Ref: group.Routine, Revision: original.Revision()}}
	presentation := stored.NewView(stored.ViewParts{Dependencies: dependencies})
	owner := *base.MustRelation(source.Ref.Address)
	rule := stored.NewRule(stored.RuleParts{
		OID: 900102, Name: "equality_rule", Event: "INSERT",
		Mode: catalog.RuleOrigin,
		Actions: []stored.RuleAction{stored.NewRuleAction(stored.RuleActionParts{
			Binding: analyze.Binding{Query: query}, Presentation: presentation,
		})},
	})
	owner.Rules = stored.RulesPayload([]stored.Rule{rule})
	state.SetDescriptor(owner.Address(), &owner)
	read := e.Catalog.Read(state)
	auth := enginetest.CatalogAuthorization(e, read)
	grafted, err := stored.GraftRuleRoutineRecipes(read, &owner, rule)
	if err != nil {
		t.Fatal(err)
	}
	root := grafted.Parts().Actions[0].Parts().Binding.Query.Root
	validate := func(root plan.Node, c exec.Context) error {
		return view.ValidateQueryJoinExecution(&c, root, read, 0)
	}
	context := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	if err := validate(root, context); err != nil {
		t.Fatal("original query-owned rule grouping rejected", err)
	}
	if distinct.Grouping[0].RoutineRecipe != nil || distinct.Grouping[0].Procedure != original {
		t.Fatal("grafting mutated source grouping")
	}
	// This context really is in the historical rule lane. An unmarked fresh
	// producer must clear it rather than borrow its generic manifest membership.
	recipe := &catalog.RuleRoutineRecipe{Owner: &owner, Rule: rule.OID, Action: 0, Presentation: presentation}
	if err := exec.SelectExpressionRecipe(&context, recipe); err != nil {
		t.Fatal(err)
	}
	if err := validate(query.Root, context); err == nil {
		t.Fatal("fresh grouping borrowed enclosing historical recipe")
	}
	for _, mutation := range []string{"original", "current", "missing", "foreign"} {
		t.Run(mutation, func(t *testing.T) {
			mapper := stored.ViewQueryRemapper{
				Catalog: read,
				Ref:     func(ref ids.ObjectRef) ids.ObjectRef { return ref },
				Type:    func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ },
				Procedure: func(p *catalog.Procedure) (*catalog.Procedure, error) {
					if p != original {
						t.Fatal("remap did not receive exact original equality declaration")
					}
					switch mutation {
					case "current":
						return &current, nil
					case "missing":
						return nil, nil
					case "foreign":
						copy := *p
						copy.Owner = engine.New().CatalogRead().Owner().Principals.Principal(p.Owner.ID())
						return &copy, nil
					}
					copy := *p
					return &copy, nil
				},
			}
			mapped, err := mapper.Nested(root)
			if err != nil {
				t.Fatal("structural mapping", err)
			}
			probe := exec.Context{EvalStatement: context.EvalStatement, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
			err = validate(mapped, probe)
			if (err == nil) != (mutation == "original") {
				t.Fatalf("%s mapped equality admission: %v", mutation, err)
			}
		})
	}
	staleOwner := owner
	staleOwner.Rules = stored.RulesPayload([]stored.Rule{rule.With(func(parts *stored.RuleParts) {
		parts.Actions = append([]stored.RuleAction(nil), parts.Actions...)
		parts.Actions[0] = parts.Actions[0].With(func(action *stored.RuleActionParts) {
			action.Presentation = stored.NewView(stored.ViewParts{Dependencies: dependencies})
		})
	})})
	stale := catalog.CloneState(state)
	stale.SetDescriptor(staleOwner.Address(), &staleOwner)
	staleAuth := enginetest.CatalogAuthorization(e, e.Catalog.Read(stale))
	if err := validate(root, exec.Context{EvalStatement: context.EvalStatement, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &staleAuth}}); err == nil {
		t.Fatal("stale original rule presentation was healed by manifest membership")
	}
}
