package stored

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ruleDisplayFixture(t *testing.T) (catalog.Read, *catalog.Relation, *catalog.Relation, RuleAction) {
	t.Helper()
	read, target, integer, _ := routineCaptureFixture(t)
	event := *target
	event.ObjectAddress.ObjectID = 910
	event.ObjectName = "source"
	read.State().SetDescriptor(event.Address(), &event)
	read.State().BindName(catalog.DescriptorNameKey(target), target.Address())
	read.State().BindName(catalog.DescriptorNameKey(&event), event.Address())
	ref := read.Owner().Domain.Ref(read.Epoch(), event.Address())
	ref.Address.SubID = 1
	column := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Origin: ref, Scope: analyze.RulePseudoScope, Frame: 2}
	action := NewRuleAction(RuleActionParts{Syntax: &parser.Insert{}, Binding: analyze.Binding{Relation: target, Ref: read.Owner().Domain.Ref(read.Epoch(), target.Address()), Targets: []int{0}, Expressions: [][]expr.Expr{{column}}}})
	return read, &event, target, action
}

func TestRuleDefinitionPresentation(t *testing.T) {
	read, event, target, action := ruleDisplayFixture(t)
	rule := NewRule(RuleParts{Name: "r", Event: "INSERT", Actions: []RuleAction{action}})
	before := rule.Parts()
	check := func(p Presentation, owner ids.SessionToken, want string) {
		t.Helper()
		got, err := RuleDefinitionSQL(context.Background(), read, event, rule, p, owner)
		if err != nil || got != want {
			t.Fatalf("definition = %q, %v; want %q", got, err, want)
		}
		if !reflect.DeepEqual(before, rule.Parts()) {
			t.Fatal("display mutated frozen rule")
		}
	}
	check(Presentation{}, ids.SessionToken{}, "CREATE RULE r AS\n    ON INSERT TO capture.source DO  INSERT INTO capture.target (v)\n  VALUES (new.v);")
	check(Presentation{Path: []ids.ObjectAddress{target.Namespace()}}, ids.SessionToken{}, "CREATE RULE r AS\n    ON INSERT TO capture.source DO  INSERT INTO target (v)\n  VALUES (new.v);")
	check(Presentation{Path: []ids.ObjectAddress{target.Namespace()}, Output: sqltype.OutputEnvironment{QuoteAllIdentifiers: true}}, ids.SessionToken{}, "CREATE RULE \"r\" AS\n    ON INSERT TO \"capture\".\"source\" DO  INSERT INTO \"target\" (\"v\")\n  VALUES (\"new\".\"v\");")
	// Publish current names in a new catalog without modifying frozen captures.
	next := catalog.CloneState(read.State())
	currentEvent, currentTarget := *event, *target
	currentEvent.ObjectName, currentTarget.ObjectName = "source_now", "target_now"
	currentEvent.Columns = append([]catalog.Column(nil), event.Columns...)
	currentTarget.Columns = append([]catalog.Column(nil), target.Columns...)
	currentEvent.Columns[0].Name, currentTarget.Columns[0].Name = "input_now", "output_now"
	next.SetDescriptor(event.Address(), &currentEvent)
	next.SetDescriptor(target.Address(), &currentTarget)
	next.BindName(catalog.DescriptorNameKey(&currentTarget), target.Address())
	read = read.WithState(next)
	check(Presentation{Path: []ids.ObjectAddress{target.Namespace()}}, ids.SessionToken{}, "CREATE RULE r AS\n    ON INSERT TO capture.source_now DO  INSERT INTO target_now (output_now)\n  VALUES (new.input_now);")
	namespace := *read.MustNamespace(event.Namespace())
	namespace.ObjectName = "pg_temp_42"
	namespace.Temporary = read.Owner().Domain.Session(7)
	next = catalog.CloneState(read.State())
	next.SetDescriptor(namespace.Address(), &namespace)
	read = read.WithState(next)
	check(Presentation{}, namespace.Temporary, "CREATE RULE r AS\n    ON INSERT TO pg_temp.source_now DO  INSERT INTO pg_temp.target_now (output_now)\n  VALUES (new.input_now);")
	check(Presentation{}, read.Owner().Domain.Session(8), "CREATE RULE r AS\n    ON INSERT TO pg_temp_42.source_now DO  INSERT INTO pg_temp_42.target_now (output_now)\n  VALUES (new.input_now);")
}

func TestRuleDefinitionReplaySeparation(t *testing.T) {
	read, event, target, action := ruleDisplayFixture(t)
	ctx := context.Background()
	rule := NewRule(RuleParts{Name: "r", Event: "INSERT", Instead: true, Actions: []RuleAction{action, action}})
	display, err := RuleDefinitionSQL(ctx, read, event, rule, Presentation{Path: []ids.ObjectAddress{target.Namespace()}}, ids.SessionToken{})
	want := "CREATE RULE r AS\n    ON INSERT TO capture.source DO INSTEAD ( INSERT INTO target (v)\n  VALUES (new.v);\n INSERT INTO target (v)\n  VALUES (new.v);\n);"
	if err != nil || display != want {
		t.Fatalf("ordered definition = %q, %v; want %q", display, err, want)
	}
	for range 2 {
		replay, err := RuleActionSQL(ctx, read, action)
		if err != nil || replay != "INSERT INTO \"capture\".\"target\" (v) VALUES (NEW.v)" {
			t.Fatal("replay changed after display", replay, err)
		}
	}
	// The frozen condition presentation, rather than source text or replay SQL,
	// owns qualification rendering. Use a real typed BOOLEAN capture.
	_, _, _, boolean := routineCaptureFixture(t)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}
	syntax := &parser.Select{Targets: []parser.Expr{raw}}
	project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Usage: boolean.Usage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{literal}, Syntax: syntax, Frame: 1}
	condition := NewView(ViewParts{Query: &plan.Query{Root: project}, Syntax: syntax, Frames: map[uint32]*plan.ProjectNode{1: project}, Queries: map[*parser.Select]*plan.ProjectNode{syntax: project}, Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{{Query: 1, Clause: analyze.ViewTargetLiteral}: literal}})
	rule = NewRule(RuleParts{Name: "qualified", Event: "UPDATE", Condition: literal, ConditionPresentation: condition})
	display, err = RuleDefinitionSQL(ctx, read, event, rule, Presentation{}, ids.SessionToken{})
	if err != nil || display != "CREATE RULE qualified AS\n    ON UPDATE TO capture.source\n   WHERE true DO NOTHING;" {
		t.Fatal("condition display", display, err)
	}
}
