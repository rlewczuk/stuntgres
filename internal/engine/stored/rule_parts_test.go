package stored

import (
	"reflect"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

// RuleParts and RuleActionParts must mirror every field of their payloads,
// and the builders and Parts must copy each one into its own twin: a field
// set alone round-trips alone, and With never changes its receiver.
func TestRulePartsRoundTrip(t *testing.T) {
	checkPartsRoundTrip(t, NewRule, Rule.Parts, Rule.With)
	checkPartsRoundTrip(t, NewRuleAction, RuleAction.Parts, RuleAction.With)
}

func TestRuleModePayloadRoundTrip(t *testing.T) {
	for _, mode := range []catalog.RuleMode{catalog.RuleOrigin, catalog.RuleDisabled, catalog.RuleReplica, catalog.RuleAlways} {
		rule := NewRule(RuleParts{OID: 900101, Name: "r", Event: "INSERT", Mode: mode})
		if rule.Mode() != mode || NewRule(rule.Parts()).Mode() != mode || rule.With(func(p *RuleParts) { p.Name = "renamed" }).Mode() != mode {
			t.Fatal("mode lost", mode)
		}
		owner := &catalog.Relation{Rules: RulesPayload([]Rule{rule})}
		if err := ValidateRules(catalog.Read{}, owner); err != nil {
			t.Fatal(err)
		}
		if err := owner.VisitRules(func(fact catalog.RuleFact) error {
			if fact.Mode != mode {
				t.Fatal(fact)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		read, event, _, _ := ruleDisplayFixture(t)
		event.Rules = owner.Rules
		mapped, err := RemapRules(read, event, nil, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil, nil)
		if err != nil || RulesOf(mapped)[0].Mode() != mode || RulesOf(event)[0].Mode() != mode {
			t.Fatal("remap lost mode or mutated source", mode, err)
		}
	}
	for _, mode := range []catalog.RuleMode{0, 'X'} {
		rule := NewRule(RuleParts{OID: 900101, Name: "r", Event: "INSERT", Mode: mode})
		if err := ValidateRules(catalog.Read{}, &catalog.Relation{Rules: RulesPayload([]Rule{rule})}); err == nil {
			t.Fatal("invalid mode admitted", mode)
		}
	}
}

func checkPartsRoundTrip[T, P any](t *testing.T, build func(P) T, parts func(T) P, with func(T, func(*P)) T) {
	t.Helper()
	payload, twins := reflect.TypeFor[T](), reflect.TypeFor[P]()
	if payload.NumField() != twins.NumField() {
		t.Fatalf("%s has %d fields, %s %d", twins, twins.NumField(), payload, payload.NumField())
	}
	for i := range payload.NumField() {
		field := payload.Field(i)
		twin, ok := twins.FieldByName(strings.ToUpper(field.Name[:1]) + field.Name[1:])
		if !ok || twin.Type != field.Type {
			t.Fatalf("%s.%s has no %s twin of type %s", payload, field.Name, twins, field.Type)
		}
	}
	var zero P
	for i := range twins.NumField() {
		var p P
		value := reflect.ValueOf(&p).Elem().Field(i)
		value.Set(nonZero(t, value.Type()))
		if got := parts(build(p)); !reflect.DeepEqual(got, p) {
			t.Fatalf("%s.%s did not round-trip: %+v", twins, twins.Field(i).Name, got)
		}
		original := build(p)
		edited := with(original, func(q *P) { *q = zero })
		if !reflect.DeepEqual(parts(original), p) || !reflect.DeepEqual(parts(edited), zero) {
			t.Fatalf("With changed its receiver for %s.%s", twins, twins.Field(i).Name)
		}
	}
}
