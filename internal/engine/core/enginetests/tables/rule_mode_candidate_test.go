package tables_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRuleModeCandidate(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	action := enginetest.RuleUnitInsert(e, sink, 1, 10)
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

func ruleUnitCatalog(t *testing.T) (*core.Engine, core.AuthorizationView, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	base := e.CatalogRead()
	typ, ok := base.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("INT4 unavailable")
	}
	makeRelation := func(id ids.OID, name string) *catalog.Relation {
		return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: name, ObjectRevision: 1, Columns: []catalog.Column{{Name: "id", Usage: typ.Usage()}, {Name: "v", Usage: typ.Usage()}}, Index: map[string]int{"id": 0, "v": 1}, Attributes: []int32{1, 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}, RelationKind: catalog.TableRelation}
	}
	src, sink := makeRelation(900001, "src"), makeRelation(900002, "sink")
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(src.Address(), src)
	next.SetDescriptor(sink.Address(), sink)
	e.Catalog.PublishLocked(next)
	return e, e.AuthorityView(access.View{Owner: &e.Catalog.Owner, Catalog: e.Catalog.Read(next)}), src, sink
}
