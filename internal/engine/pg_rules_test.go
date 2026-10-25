package engine

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPGRulesProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	first := inheritanceUnitRelation(f.e, "rules_a", 23001, "id")
	second := inheritanceUnitRelation(f.e, "rules_b", 23002, "id")
	first.Owner, second.Owner = f.owner, f.owner
	first.Security.ACL = authorization.ACLValue{Explicit: true}
	first.Rules = stored.RulesPayload([]stored.Rule{
		stored.NewRule(stored.RuleParts{OID: 23003, Name: "r", Event: "INSERT", Instead: true}),
		// Invalid actions deliberately establish exclusion before rendering.
		stored.NewRule(stored.RuleParts{OID: 23004, Name: "_RETURN", Event: "SELECT", Actions: []stored.RuleAction{stored.NewRuleAction(stored.RuleActionParts{})}}),
		stored.NewRule(stored.RuleParts{OID: 23005, Name: "_return", Event: "DELETE"}),
	})
	second.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 23006, Name: "r", Event: "UPDATE"})})
	view := inheritanceUnitView(f.e, first, second)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.RulesOID})
	produce, ok := pgcatalog.ProducerFor(pgcatalog.RulesOID)
	if !ok {
		t.Fatal("missing rule producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Runtime: EngineCatalogRuntime{presentation: stored.Presentation{Deparse: true}, Auth: AuthorizationView{}}}
	rows, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	var got [][]string
	for _, row := range rows {
		if len(row) != 4 {
			t.Fatal("rule width", row)
		}
		cells := make([]string, 4)
		for i, value := range row {
			if value.Type() != source.Columns[i].Usage.Type || !value.ValidResult(source.Columns[i].Usage) || value.IsNull() {
				t.Fatal("rule cell descriptor", i, value)
			}
			cells[i], err = value.Text()
			if err != nil {
				t.Fatal(err)
			}
		}
		got = append(got, cells)
	}
	sort.Slice(got, func(i, j int) bool { return got[i][1]+got[i][2] < got[j][1]+got[j][2] })
	want := [][]string{
		{"public", "rules_a", "_return", "CREATE RULE _return AS\n    ON DELETE TO public.rules_a DO NOTHING;"},
		{"public", "rules_a", "r", "CREATE RULE r AS\n    ON INSERT TO public.rules_a DO INSTEAD NOTHING;"},
		{"public", "rules_b", "r", "CREATE RULE r AS\n    ON UPDATE TO public.rules_b DO NOTHING;"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rules: %#v, want %#v", got, want)
	}
	// No authorization capability was installed: explicit-empty table ACLs do
	// not hide public metadata, and stored same-named rules remain separate.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = canceled
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled prefix", rows, err)
	}
	lateContext, lateCancel := context.WithCancel(context.Background())
	defer lateCancel()
	compact := catalog.NewState(view.Epoch())
	compact.SetDescriptor(first.Namespace(), view.MustNamespace(first.Namespace()))
	compact.SetDescriptor(first.Address(), first)
	input.Catalog = view.WithState(compact)
	input.Context = &cancelAtErrCheck{Context: lateContext, cancel: lateCancel, at: 7}
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("late cancellation exposed prefix", rows, err)
	}
	input.Context = context.Background()
	broken := *first
	broken.Rules = stored.RulesPayload([]stored.Rule{
		stored.NewRule(stored.RuleParts{OID: 23003, Name: "r", Event: "INSERT"}),
		stored.NewRule(stored.RuleParts{OID: 23007, Name: "broken", Event: "INSERT", Actions: []stored.RuleAction{stored.NewRuleAction(stored.RuleActionParts{Syntax: &parser.Insert{}, Binding: analyze.Binding{Ref: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 23999}}}})}}),
	})
	input.Catalog = inheritanceUnitView(f.e, &broken)
	if rows, err := produce(input); rows != nil || !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("failed render exposed prefix", rows, err)
	}
	input.Catalog = f.e.CatalogRead()
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("empty source", rows, err)
	}
	if source.Columns[0].Usage.Type.ID() != sqltype.Name || source.Columns[3].Usage.Type.ID() != sqltype.Text {
		t.Fatal("rule logical types")
	}
}
