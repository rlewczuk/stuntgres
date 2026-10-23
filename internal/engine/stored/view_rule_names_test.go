package stored

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
)

func TestRuleRangeTableAllocation(t *testing.T) {
	t.Run("later_explicit_alias_is_renamed", func(t *testing.T) {
		var table ruleRangeTable
		first := table.allocate(ruleRenderAlias{1, analyze.ViewDerivedNamespace, 2}, "unnamed_subquery", nil)
		raw := &parser.AliasReference{Name: parser.Identifier{Text: "unnamed_subquery"}, Columns: []parser.Identifier{{Text: "kept_column"}}}
		key := ruleRenderAlias{1, analyze.ViewSourceNamespace, 0}
		second := table.allocate(key, raw.Name.Text, raw)
		if first.name != "unnamed_subquery" || second.name != "unnamed_subquery_1" || !second.explicit || second.columns[0].Text != "kept_column" {
			t.Fatalf("ordered occurrences = %#v, %#v", first, second)
		}
		// Re-requesting an occurrence must retain its original declaration facts.
		if again := table.allocate(key, "other", nil); again.name != second.name || !again.explicit || again.columns[0].Text != "kept_column" {
			t.Fatalf("reallocated occurrence = %#v", again)
		}
		if raw.Name.Text != "unnamed_subquery" || raw.Columns[0].Text != "kept_column" {
			t.Fatal("allocation changed retained alias syntax")
		}
	})
	t.Run("ancestor_names_and_existing_suffix", func(t *testing.T) {
		table := ruleRangeTable{used: map[string]int{"walk": 0, "walk_1": 0}}
		first := table.allocate(ruleRenderAlias{3, analyze.ViewDerivedNamespace, 0}, "walk", nil)
		second := table.allocate(ruleRenderAlias{3, analyze.ViewDerivedNamespace, 1}, "walk", nil)
		if first.name != "walk_2" || second.name != "walk_3" {
			t.Fatalf("ancestor collisions = %q, %q", first.name, second.name)
		}
	})
	t.Run("utf8_name_limit", func(t *testing.T) {
		base := strings.Repeat("é", 31) + "x"
		table := ruleRangeTable{used: map[string]int{base: 0}}
		got := table.allocate(ruleRenderAlias{1, analyze.ViewDerivedNamespace, 0}, base, nil)
		want := strings.Repeat("é", 30) + "_1"
		if got.name != want || !utf8.ValidString(got.name) || len(got.name) > 63 {
			t.Fatalf("clipped name = %q; want %q", got.name, want)
		}
	})
}

func TestRuleModifierNameContext(t *testing.T) {
	read, _, target, action := ruleDisplayFixture(t)
	parent := ruleRendering{RuleAction: action, policy: Presentation{Deparse: true, Rule: true}}
	if err := parent.prepareNamespaces(context.Background(), read); err != nil {
		t.Fatal(err)
	}
	if parent.names.pseudoName(true) != "old" || parent.names.pseudoName(false) != "new" || parent.names.target != "target" {
		t.Fatalf("parent namespace = %#v", parent.names)
	}
	child := ruleRendering{RuleAction: action, policy: parent.policy, parentNames: parent.names}
	if err := child.prepareNamespaces(context.Background(), read); err != nil {
		t.Fatal(err)
	}
	if child.targetDeclaration("target") != "target AS target_1" || child.targetQualifier(target) != "target_1" {
		t.Fatalf("child target declaration/reference disagree: %q, %q", child.targetDeclaration("target"), child.targetQualifier(target))
	}
	if child.names.pseudoName(true) != "old" || child.names.pseudoName(false) != "new" {
		t.Fatalf("child reallocated inherited pseudo RTEs: %#v", child.names)
	}
	if _, changed := parent.names.used["target_1"]; changed || parent.targetAlias != "" {
		t.Fatal("child allocation mutated the parent namespace")
	}
}
