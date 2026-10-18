package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// The payload seams keep "absent" a nil interface and hand the concrete
// engine definition back only through their checked accessor.
func TestCatalogPayloadAccessors(t *testing.T) {
	if viewOf(&catalog.Relation{}) != nil || viewPayloadOf(nil) != nil {
		t.Fatal("absent view payload")
	}
	domain := &ids.Domain{}
	consumer := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}
	source := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}
	column := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20003, SubID: 2}
	captured := &catalog.Relation{ObjectAddress: source}
	ref := domain.Ref(7, source)
	token := domain.Session(5)
	definition := &viewDefinition{rule: 30001, consumer: consumer, temporary: token, sources: []viewLeaf{{ref: ref}}, dependencies: []catalog.ExpressionDependency{{Ref: domain.Ref(7, column)}}, references: map[ids.ObjectRef]catalog.Descriptor{ref: captured}}
	d := &catalog.Relation{ObjectAddress: consumer, View: viewPayloadOf(definition)}
	if viewOf(d) != definition || d.View.RuleOID() != 30001 {
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

	if policyExpressionOf(nil) != nil || policyPayloadOf(nil) != nil {
		t.Fatal("absent policy payload")
	}
	condition := &policyExpression{presentation: definition}
	policy := catalog.PolicyDefinition{Using: policyPayloadOf(condition)}
	if policyExpressionOf(policy.Using) != condition || policy.Check != nil {
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
