package stored

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func TestRuleDefinitionReturningLabels(t *testing.T) {
	read, event, target, action := ruleDisplayFixture(t)
	ref := read.Owner().Domain.Ref(read.Epoch(), target.Address())
	ref.Address.SubID = target.Attributes[0]
	column := plan.ColumnExpr{Info: expr.Info{Usage: target.Columns[0].Usage}, Origin: ref}
	action = action.With(func(p *RuleActionParts) {
		p.Binding.Returning = &analyze.Returning{Expressions: []expr.Expr{column}, Schema: []catalog.Column{{Name: "copied", Usage: target.Columns[0].Usage}}}
	})
	rule := NewRule(RuleParts{Name: "r", Event: "INSERT", Instead: true, Actions: []RuleAction{action}})
	ctx := context.Background()
	for range 2 {
		display, err := RuleDefinitionSQL(ctx, read, event, rule, Presentation{Path: []ids.ObjectAddress{target.Namespace()}}, ids.SessionToken{})
		want := "CREATE RULE r AS\n    ON INSERT TO capture.source DO INSTEAD  INSERT INTO target (v)\n  VALUES (new.v)\n  RETURNING target.v AS copied;"
		if err != nil || display != want {
			t.Fatalf("display = %q, %v; want %q", display, err, want)
		}
		replay, err := RuleActionSQL(ctx, read, action)
		if err != nil || replay != "INSERT INTO \"capture\".\"target\" (v) VALUES (NEW.v) RETURNING \"target\".v" {
			t.Fatal("replay contract changed", replay, err)
		}
	}
	aliased := action.With(func(p *RuleActionParts) {
		p.Syntax = &parser.Insert{Alias: parser.Identifier{Text: "Kept Alias"}}
	})
	aliasedRule := rule.With(func(p *RuleParts) { p.Actions = []RuleAction{aliased} })
	for range 2 {
		display, err := RuleDefinitionSQL(ctx, read, event, aliasedRule, Presentation{Path: []ids.ObjectAddress{target.Namespace()}}, ids.SessionToken{})
		want := "CREATE RULE r AS\n    ON INSERT TO capture.source DO INSTEAD  INSERT INTO target AS \"Kept Alias\" (v)\n  VALUES (new.v)\n  RETURNING \"Kept Alias\".v AS copied;"
		if err != nil || display != want {
			t.Fatalf("aliased display = %q, %v; want %q", display, err, want)
		}
		replay, err := RuleActionSQL(ctx, read, aliased)
		if err != nil || replay != "INSERT INTO \"capture\".\"target\" (v) VALUES (NEW.v) RETURNING \"target\".v" {
			t.Fatal("alias display changed replay", replay, err)
		}
	}
	if action.syntax.(*parser.Insert).Alias.Text != "" || aliased.syntax.(*parser.Insert).Alias.Text != "Kept Alias" {
		t.Fatal("target presentation mutated retained syntax")
	}
	if got := action.binding.Returning.Schema[0].Name; got != "copied" {
		t.Fatal("display mutated retained RETURNING labels", got)
	}
}

func TestRuleDefinitionReturningSides(t *testing.T) {
	read, event, target, action := ruleDisplayFixture(t)
	statements, err := parser.Parse("INSERT INTO target VALUES (1) RETURNING WITH (NEW AS after, OLD AS before) before.v AS previous, after.v AS replacement")
	if err != nil {
		t.Fatal(err)
	}
	syntax := statements[0].(*parser.Insert)
	ref := read.Owner().Domain.Ref(read.Epoch(), target.Address())
	ref.Address.SubID = target.Attributes[0]
	old := plan.ColumnExpr{Info: expr.Info{Usage: target.Columns[0].Usage, Pos: syntax.Returning.Targets[0].SourcePosition()}, Origin: ref, Scope: 1, Frame: 1}
	new := old
	new.Info.Pos = syntax.Returning.Targets[1].SourcePosition()
	action = action.With(func(p *RuleActionParts) {
		p.Syntax = syntax
		p.Binding.Returning = &analyze.Returning{
			Expressions: []expr.Expr{plan.ReturningColumnExpr{ColumnExpr: old, Old: true}, new},
			Schema:      []catalog.Column{{Name: "previous", Usage: old.Usage}, {Name: "replacement", Usage: new.Usage}},
		}
	})
	rule := NewRule(RuleParts{Name: "r", Event: "INSERT", Instead: true, Actions: []RuleAction{action}})
	for range 2 {
		display, err := RuleDefinitionSQL(context.Background(), read, event, rule, Presentation{Path: []ids.ObjectAddress{target.Namespace()}}, ids.SessionToken{})
		want := "CREATE RULE r AS\n    ON INSERT TO capture.source DO INSTEAD  INSERT INTO target (v)\n  VALUES (new.v)\n  RETURNING WITH (OLD AS before, NEW AS after) before.v AS previous,\n    after.v AS replacement;"
		if err != nil || display != want {
			t.Fatalf("display = %q, %v; want %q", display, err, want)
		}
		replay, err := RuleActionSQL(context.Background(), read, action)
		if err != nil || replay != "INSERT INTO \"capture\".\"target\" (v) VALUES (NEW.v) RETURNING \"target\".v, \"target\".v" {
			t.Fatal("side presentation changed replay", replay, err)
		}
	}
	if syntax.Returning.Aliases[0].Alias.Text != "after" || syntax.Returning.Aliases[1].Alias.Text != "before" {
		t.Fatal("display reordered retained RETURNING options")
	}
	if !action.binding.Returning.Expressions[0].(plan.ReturningColumnExpr).Old {
		t.Fatal("display erased retained old-side provenance")
	}
}
