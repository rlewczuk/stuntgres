package sequence

import (
	"errors"
	"strconv"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// AlterEffects bumps the plan stamp for any option but OWNED BY and for a
// name, schema, owner or persistence change; only options and persistence
// replace the generator; a plain option list or persistence change drops the
// caller's tail.
func TestSequenceAlterEffects(t *testing.T) {
	old := callRelation()
	same := func(change func(*catalog.Relation)) *catalog.Relation {
		next := *old
		options := *old.Sequence
		next.Sequence = &options
		change(&next)
		return &next
	}
	owned := []parser.SequenceOption{{Name: "owned"}}
	restart := []parser.SequenceOption{{Name: "restart"}}
	cases := []struct {
		label  string
		next   *catalog.Relation
		raw    []parser.SequenceOption
		action string
		want   Effects
	}{
		{"owned by only", same(func(*catalog.Relation) {}), owned, "", Effects{DiscardTail: true}},
		{"option", same(func(*catalog.Relation) {}), restart, "", Effects{PlanStamp: true, ReplaceGenerator: true, DiscardTail: true}},
		{"rename", same(func(n *catalog.Relation) { n.ObjectName = "t" }), nil, "rename", Effects{PlanStamp: true}},
		{"owner no-op", same(func(*catalog.Relation) {}), nil, "owner", Effects{}},
		{"unlogged", same(func(n *catalog.Relation) { n.Sequence.Persistence = "unlogged" }), nil, "unlogged", Effects{PlanStamp: true, ReplaceGenerator: true, DiscardTail: true}},
	}
	for _, c := range cases {
		if got := AlterEffects(old, c.next, c.raw, c.action); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.label, got, c.want)
		}
	}
}

// SplitIdentityOptions keeps the AS option first, rejects repeats at the
// repeated option and defers the TEMP persistence check to CheckTemporary.
func TestSequenceSplitIdentityOptions(t *testing.T) {
	as := parser.SequenceOption{Name: "as", Type: parser.TypeName{Name: "integer"}}
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "s"}}}
	split, err := SplitIdentityOptions(as, []parser.SequenceOption{{Name: "unlogged", Pos: parser.Position{Character: 4}}, {Name: "start", Value: 3}, {Name: "name", Owned: name}})
	if err != nil || split.Persistence != "unlogged" || len(split.Raw) != 2 || split.Raw[0].Name != "as" || split.Raw[0].Type.Name != "integer" || split.Raw[1].Name != "start" || len(split.Name.Parts) != 1 {
		t.Fatal("split", split, err)
	}
	wantPosition(t, split.CheckTemporary(), "42P16", 4)
	_, err = SplitIdentityOptions(as, []parser.SequenceOption{{Name: "logged"}, {Name: "unlogged", Pos: parser.Position{Character: 9}}})
	wantPosition(t, err, "42601", 9)
	_, err = SplitIdentityOptions(as, []parser.SequenceOption{{Name: "name", Owned: name}, {Name: "name", Owned: name, Pos: parser.Position{Character: 12}}})
	wantPosition(t, err, "42601", 12)
	if plain, err := SplitIdentityOptions(as, nil); err != nil || plain.Persistence != "logged" || plain.CheckTemporary() != nil {
		t.Fatal("default persistence", plain, err)
	}
}

func wantPosition(t *testing.T, err error, code string, character int) {
	t.Helper()
	var e *sqlstate.Error
	if !errors.As(err, &e) || e.Code != code || e.Position != strconv.Itoa(character) {
		t.Fatalf("got %v, want %s at %d", err, code, character)
	}
}
