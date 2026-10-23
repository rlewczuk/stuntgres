package catalog

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

type fakeRules []RuleFact

func (r fakeRules) Validate(Read, *Relation) error         { return nil }
func (r fakeRules) Len() int                               { return len(r) }
func (r fakeRules) RoutineRecipe(ids.OID, int) ViewPayload { return nil }
func (r fakeRules) Visit(visit func(RuleFact) error) error {
	for _, rule := range r {
		if err := visit(rule); err != nil {
			return err
		}
	}
	return nil
}

type fakeCondition struct{ expression expr.Expr }

func (c fakeCondition) DependsOn(ids.ObjectAddress) bool                  { return false }
func (c fakeCondition) Validate(Read, *Relation) error                    { return nil }
func (c fakeCondition) VisitExpression(visit func(expr.Expr) error) error { return visit(c.expression) }

// fakeExpr is an expression identity only; the readers never evaluate it.
type fakeExpr struct {
	expr.Expr
	name string
}

// Relation and policy readers tolerate absent payloads and keep the payload
// order: rules as stored, then USING before WITH CHECK.
func TestPayloadReadersOnDescriptors(t *testing.T) {
	if (&Relation{}).HasRules() || (&Relation{Rules: fakeRules{}}).HasRules() {
		t.Fatal("a relation without rules reported rules")
	}
	rules := fakeRules{{OID: 1, Name: "a"}, {OID: 2, Name: "b"}}
	relation := &Relation{Rules: rules}
	if !relation.HasRules() {
		t.Fatal("a relation with rules reported none")
	}
	var names []string
	collect := func(rule RuleFact) error { names = append(names, rule.Name); return nil }
	if err := (&Relation{}).VisitRules(collect); err != nil || names != nil {
		t.Fatal("absent rules visited", names, err)
	}
	if err := relation.VisitRules(collect); err != nil || !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Fatal("rule order", names, err)
	}
	stop := errors.New("stop")
	if err := relation.VisitRules(func(RuleFact) error { return stop }); err != stop {
		t.Fatal("rule visit error", err)
	}

	using, check := fakeExpr{name: "using"}, fakeExpr{name: "check"}
	var seen []expr.Expr
	visit := func(e expr.Expr) error { seen = append(seen, e); return nil }
	if err := (PolicyDefinition{}).VisitExpressions(visit); err != nil || seen != nil {
		t.Fatal("absent conditions visited", seen, err)
	}
	if err := (PolicyDefinition{Check: fakeCondition{check}}).VisitExpressions(visit); err != nil || !reflect.DeepEqual(seen, []expr.Expr{check}) {
		t.Fatal("check only", seen, err)
	}
	seen = nil
	if err := (PolicyDefinition{Using: fakeCondition{using}, Check: fakeCondition{check}}).VisitExpressions(visit); err != nil || !reflect.DeepEqual(seen, []expr.Expr{using, check}) {
		t.Fatal("condition order", seen, err)
	}
	seen = nil
	if err := (PolicyDefinition{Using: fakeCondition{using}, Check: fakeCondition{check}}).VisitExpressions(func(e expr.Expr) error { seen = append(seen, e); return stop }); err != stop || len(seen) != 1 {
		t.Fatal("condition visit must stop at the first error", seen, err)
	}
}
