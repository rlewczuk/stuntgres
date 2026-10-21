package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestRuleViewExpressionDependencyClosure(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE DOMAIN rule_domain AS INT CHECK(VALUE>=0)",
		"CREATE TABLE base(id INT)", "CREATE VIEW event_v AS SELECT id FROM base", "CREATE TABLE sink(id INT)",
		"CREATE RULE r AS ON INSERT TO event_v DO INSTEAD INSERT INTO sink VALUES(NEW.id::rule_domain)",
		"CREATE RULE keep_r AS ON DELETE TO event_v DO ALSO NOTHING",
	} {
		sessionSQL(t, s, q)
	}
	view := e.CatalogRead()
	eventRef := catalogRef(t, e, view, "event_v")
	baseRef := catalogRef(t, e, view, "base")
	event := view.MustRelation(eventRef.Address)
	var provider ids.ObjectAddress
	for address, descriptor := range view.Descriptors() {
		if address.ClassID == ids.TypeClassID && descriptor.Name() == "rule_domain" {
			provider = address
			break
		}
	}
	if provider == (ids.ObjectAddress{}) {
		t.Fatal("missing analyzed domain fixture")
	}
	var ruleOID ids.OID
	for _, rule := range RulesOf(event) {
		if rule.Name == "r" {
			ruleOID = rule.OID
		}
	}
	if ruleOID == 0 {
		t.Fatal("missing analyzed ordinary rule fixture")
	}
	if viewDepends(event, provider) {
		t.Fatal("domain entered the view's unrelated SELECT definition")
	}
	closure := func(target ids.ObjectAddress) []domainDependency {
		t.Helper()
		edges, err := domainDependencyClosure(context.Background(), e.BeginCatalogStage(view), []ids.ObjectAddress{target})
		if err != nil {
			t.Fatal(err)
		}
		return edges
	}
	ruleAddress := ids.ObjectAddress{ClassID: 2618, ObjectID: ruleOID}
	found := false
	for _, edge := range closure(provider) {
		if edge.address == event.Address() {
			t.Fatal("ordinary rule dependency retired its event view", edge)
		}
		if edge.address == ruleAddress {
			found = true
			if edge.target != provider || edge.root != provider || edge.relation != event || edge.automatic {
				t.Fatal("ordinary rule edge lost object/owner/provider authority", edge)
			}
		}
	}
	if !found {
		t.Fatal("central expression closure omitted ordinary view-attached rule")
	}
	// The separately analyzed SELECT definition remains the event relation's edge.
	foundView := false
	for _, edge := range closure(baseRef.Address) {
		if edge.address == event.Address() {
			foundView = edge.target == baseRef.Address && edge.root == baseRef.Address
		}
	}
	if !foundView {
		t.Fatal("view SELECT definition disappeared from the same canonical closure")
	}
	stage := e.BeginCatalogStage(view)
	if err := stageDropProvider(context.Background(), stage, nil, nil, nil).applyViewDependents([]ids.ObjectAddress{provider}, parser.DropCascade); err != nil {
		t.Fatal(err)
	}
	current := e.stageRead(stage).MustRelation(event.Address())
	if current.View != event.View || len(RulesOf(current)) != 1 || RulesOf(current)[0].Name != "keep_r" {
		t.Fatal("rule-only retirement changed SELECT identity or unrelated ordinary rule")
	}
	if len(RulesOf(event)) != 2 {
		t.Fatal("staged retirement mutated published event rules")
	}
}
