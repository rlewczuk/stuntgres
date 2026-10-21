package engine

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// The payload seams keep "absent" a nil interface and hand the concrete
// engine definition back only through their checked accessor.
func TestCatalogPayloadAccessors(t *testing.T) {
	if ViewOf(&catalog.Relation{}) != nil || ViewPayload(nil) != nil {
		t.Fatal("absent view payload")
	}
	domain := &ids.Domain{}
	consumer := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}
	source := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}
	column := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20003, SubID: 2}
	captured := &catalog.Relation{ObjectAddress: source}
	ref := domain.Ref(7, source)
	token := domain.Session(5)
	definition := NewView(ViewParts{Rule: 30001, Consumer: consumer, Temporary: token, Sources: []viewLeaf{{Ref: ref}}, Dependencies: []catalog.ExpressionDependency{{Ref: domain.Ref(7, column)}}, References: map[ids.ObjectRef]catalog.Descriptor{ref: captured}})
	d := &catalog.Relation{ObjectAddress: consumer, View: ViewPayload(definition)}
	if ViewOf(d) != definition || d.View.RuleOID() != 30001 {
		t.Fatal("view payload identity")
	}
	if owner, session := d.View.TemporaryConsumer(); owner != consumer || session != token {
		t.Fatal("temporary consumer", owner, session)
	}
	if got, ok := d.View.CapturedReference(ref); !ok || got != captured {
		t.Fatal("captured reference")
	}
	if _, ok := d.View.CapturedReference(domain.Ref(8, source)); ok {
		t.Fatal("uncaptured reference")
	}
	whole := column
	whole.SubID = 0
	other := column
	other.SubID = 3
	for target, want := range map[ids.ObjectAddress]bool{source: true, whole: true, column: true, other: false, consumer: false} {
		if d.View.DependsOn(target) != want || viewDepends(d, target) != want {
			t.Fatal("view dependency", target, want)
		}
	}
	if viewDepends(&catalog.Relation{}, source) {
		t.Fatal("absent view has dependencies")
	}

	if PolicyOf(nil) != nil || policyPayloadOf(nil) != nil {
		t.Fatal("absent policy payload")
	}
	condition := NewPolicy(nil, nil, definition)
	policy := catalog.PolicyDefinition{Using: policyPayloadOf(condition)}
	if PolicyOf(policy.Using) != condition || policy.Check != nil {
		t.Fatal("policy payload identity")
	}
	for target, want := range map[ids.ObjectAddress]bool{source: true, column: true, other: false} {
		if policy.Using.DependsOn(target) != want || policyDepends(policy, target) != want {
			t.Fatal("policy dependency", target, want)
		}
	}

	if rulesOf(&catalog.Relation{}) != nil || rulesPayloadOf(nil) != nil {
		t.Fatal("absent rules payload")
	}
	empty := &catalog.Relation{Rules: rulesPayloadOf([]ruleDefinition{})}
	if empty.Rules == nil || rulesOf(empty) == nil || len(rulesOf(empty)) != 0 {
		t.Fatal("empty rule list collapsed")
	}
	rules := []ruleDefinition{{OID: 40001, Name: "a"}, {OID: 40002, Name: "b"}}
	withRules := &catalog.Relation{Rules: rulesPayloadOf(rules)}
	if got := rulesOf(withRules); len(got) != 2 || &got[0] != &rules[0] {
		t.Fatal("rule list identity")
	}
	if validateRules(catalog.Read{}, &catalog.Relation{}) != nil {
		t.Fatal("absent rules rejected")
	}
	rules[1].Name = "a"
	if withRules.Rules.Validate(catalog.Read{}, withRules) == nil {
		t.Fatal("unordered rule names accepted")
	}
}

// The payload readers give non-root callers what they used to ask the host
// and pgcatalog runtime for: rule facts in order, view options and policy
// conditions.
func TestCatalogPayloadReaders(t *testing.T) {
	refs := []ids.ObjectAddress{{ClassID: ids.RelationClassID, ObjectID: 20002}}
	rules := ruleList{{OID: 40001, Name: "a", event: "UPDATE", instead: true, references: refs}, {OID: 40002, Name: "b", event: "DELETE"}}
	if rules.Len() != 2 || (ruleList{}).Len() != 0 {
		t.Fatal("rule count")
	}
	var facts []catalog.RuleFact
	if err := rules.Visit(func(rule catalog.RuleFact) error { facts = append(facts, rule); return nil }); err != nil {
		t.Fatal(err)
	}
	want := []catalog.RuleFact{{OID: 40001, Name: "a", Event: "2", Instead: true, References: refs}, {OID: 40002, Name: "b", Event: "4"}}
	if !reflect.DeepEqual(facts, want) {
		t.Fatal("rule facts", facts)
	}
	stop := errors.New("stop")
	visited := 0
	if err := rules.Visit(func(catalog.RuleFact) error { visited++; return stop }); err != stop || visited != 1 {
		t.Fatal("rule visit must stop at the first error", err, visited)
	}

	options := []string{"security_barrier=on"}
	view := NewView(ViewParts{Options: options})
	if got := ViewPayload(view).Options(); len(got) != 1 || &got[0] != &options[0] {
		t.Fatal("view options", got)
	}
	if (NewView(ViewParts{})).Options() != nil {
		t.Fatal("view without options")
	}

	condition := plan.LiteralExpr{}
	var seen []expr.Expr
	if err := policyPayloadOf(NewPolicy(nil, condition, nil)).VisitExpression(func(e expr.Expr) error { seen = append(seen, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], expr.Expr(condition)) {
		t.Fatal("policy expression", seen)
	}
	if err := policyPayloadOf(NewPolicy(nil, condition, nil)).VisitExpression(func(expr.Expr) error { return stop }); err != stop {
		t.Fatal("policy visit error", err)
	}
}
