package core_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func TestRuleModeCandidate(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	action := ruleUnitInsert(e, sink, 1, 10)
	rule := stored.NewRule(stored.RuleParts{OID: 900101, Name: "r", Event: "INSERT", Mode: catalog.RuleOrigin, Actions: []stored.RuleAction{action}, References: []ids.ObjectAddress{sink.Address()}})
	src.Rules = stored.RulesPayload([]stored.Rule{rule})
	before := rule.Parts()
	original := src
	for _, tc := range []struct {
		kind parser.AlterTableActionKind
		mode catalog.RuleMode
	}{{parser.DisableRule, catalog.RuleDisabled}, {parser.EnableReplicaRule, catalog.RuleReplica}, {parser.EnableAlwaysRule, catalog.RuleAlways}, {parser.EnableRule, catalog.RuleOrigin}} {
		next, err := table.RuleModeCandidate(src, parser.AlterTableAction{Action: tc.kind, Rule: parser.Identifier{Text: "r"}})
		if err != nil || next == src || next.Revision() != src.Revision() {
			t.Fatal(next, err)
		}
		got := stored.RulesOf(next)[0]
		parts := got.Parts()
		parts.Mode = before.Mode
		if got.Mode() != tc.mode || !reflect.DeepEqual(parts, before) || &got.Actions()[0] != &rule.Actions()[0] || &got.References()[0] != &rule.References()[0] {
			t.Fatal("mode change replaced borrowed recipes", tc.mode)
		}
		same, err := table.RuleModeCandidate(next, parser.AlterTableAction{Action: tc.kind, Rule: parser.Identifier{Text: "r"}})
		if err != nil || same != next {
			t.Fatal("no-op changed descriptor backing", err)
		}
		if !reflect.DeepEqual(stored.RulesOf(original)[0].Parts(), before) || original != auth.Catalog.MustRelation(src.Address()) {
			t.Fatal("candidate mutated published predecessor")
		}
		src = next
	}
	_, err := table.RuleModeCandidate(src, parser.AlterTableAction{Action: parser.DisableRule, Rule: parser.Identifier{Text: "missing"}})
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "42704" {
		t.Fatal(err)
	}
	exhausted := *src
	exhausted.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	next, err := table.RuleModeCandidate(&exhausted, parser.AlterTableAction{Action: parser.DisableRule, Rule: parser.Identifier{Text: "r"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := table.BumpAlterRevision(&exhausted, next); err == nil || exhausted.Revision() != ids.DefinitionRevision(^uint64(0)) || stored.RulesOf(&exhausted)[0].Mode() != catalog.RuleOrigin {
		t.Fatal("revision overflow accepted or mutated predecessor", err)
	}
}
