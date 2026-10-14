package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPgClassOptionHistory(t *testing.T) {
	option := func(name, value string) parser.ViewOption {
		return parser.ViewOption{Name: parser.Identifier{Text: name}, Value: value, HasValue: true}
	}
	created := []parser.ViewOption{option("security_invoker", "false"), option("security_barrier", "true")}
	barrier, invoker, mode, err := viewOptions(created)
	if err != nil || !barrier || invoker || mode != viewCheckNone {
		t.Fatal("execution flags", barrier, invoker, mode, err)
	}
	old := transformViewOptions(nil, created, false)
	if !reflect.DeepEqual(old, []string{"security_invoker=false", "security_barrier=true"}) {
		t.Fatal(old)
	}
	set := transformViewOptions(old, []parser.ViewOption{option("security_invoker", "off")}, false)
	if !reflect.DeepEqual(set, []string{"security_barrier=true", "security_invoker=off"}) || !reflect.DeepEqual(old, []string{"security_invoker=false", "security_barrier=true"}) {
		t.Fatal("SET ordering/immutability", set, old)
	}
	reset := transformViewOptions(set, []parser.ViewOption{{Name: parser.Identifier{Text: "security_barrier"}}}, true)
	if !reflect.DeepEqual(reset, []string{"security_invoker=off"}) {
		t.Fatal(reset)
	}
	if got := transformViewOptions(reset, []parser.ViewOption{{Name: parser.Identifier{Text: "security_invoker"}}}, true); len(got) != 0 {
		t.Fatal("RESET final option", got)
	}
	if got := transformViewOptions(nil, nil, false); len(got) != 0 {
		t.Fatal("replacement absent options", got)
	}
	lexical := []parser.ViewOption{option("security_invoker", "0"), option("security_barrier", "on"), option("check_option", "local")}
	if got := transformViewOptions(nil, lexical, false); !reflect.DeepEqual(got, []string{"security_invoker=0", "security_barrier=on", "check_option=local"}) {
		t.Fatal("lexical facts", got)
	}
	e := New()
	original := &viewDefinition{query: &BoundQuery{Root: &singletonNode{}}, options: old, barrier: true, invoker: false, checkOption: viewCheckNone, rule: 200001}
	mapped, err := remapFrozenPresentation(e.CatalogRead(), original, nil, func(ref ObjectRef) ObjectRef { ref.Epoch++; return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil)
	if err != nil || mapped == original || mapped.rule != original.rule || mapped.barrier != original.barrier || mapped.invoker != original.invoker || !reflect.DeepEqual(mapped.options, old) {
		t.Fatal("remap lost installed options", mapped, err)
	}
	mapped.options = transformViewOptions(mapped.options, []parser.ViewOption{option("security_invoker", "true")}, false)
	if !reflect.DeepEqual(original.options, old) || !reflect.DeepEqual(mapped.options, []string{"security_barrier=true", "security_invoker=true"}) {
		t.Fatal("remapped mutation touched retained facts")
	}
}

func TestPgClassPartitionTree(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	integer, ok := catalog.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("INT4")
	}
	key := partitionKeyEntry{usage: integer.Usage()}
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	membership := &partitionMembership{lower: []partitionDatum{{value: value(0)}}, upper: []partitionDatum{{value: value(10)}}}
	tree := partitionBoundTree{membership: membership, keys: []partitionKeyEntry{key}}
	want := `{PartitionBound strategy range default false lower ({type 23 typmod -1 collation 0 value "0"}) upper ({type 23 typmod -1 collation 0 value "10"})}`
	if got, err := tree.StructuralOutput(context.Background()); err != nil || got != want {
		t.Fatal(got, err)
	}
	// Replacing the descriptor's topology does not retarget an already captured tree.
	relation := &relationDescriptor{membership: membership}
	relation.membership = &partitionMembership{defaultBound: true}
	if got, err := tree.StructuralOutput(context.Background()); err != nil || got != want {
		t.Fatal("captured tree retargeted", got, err)
	}
	defaultTree := partitionBoundTree{membership: relation.membership}
	if got, err := defaultTree.StructuralOutput(context.Background()); err != nil || got != "{PartitionBound strategy range default true}" {
		t.Fatal(got, err)
	}
	multi := partitionBoundTree{keys: []partitionKeyEntry{key, key}, membership: &partitionMembership{lower: []partitionDatum{{kind: -1}, {kind: -1}}, upper: []partitionDatum{{kind: 1}, {kind: 1}}}}
	if got, err := multi.StructuralOutput(context.Background()); err != nil || got != "{PartitionBound strategy range default false lower (MINVALUE MINVALUE) upper (MAXVALUE MAXVALUE)}" {
		t.Fatal(got, err)
	}
	character, ok := catalog.LookupType(sqltype.VarChar)
	if !ok {
		t.Fatal("VARCHAR")
	}
	usage := character.Usage()
	usage.Typmod = 14
	usage.Collation.ID = 950
	quoted := partitionBoundTree{keys: []partitionKeyEntry{{usage: usage}}, membership: &partitionMembership{lower: []partitionDatum{{value: sqltype.String(character, "a\"b")}}, upper: []partitionDatum{{value: sqltype.String(character, "z")}}}}
	wantQuoted := `{PartitionBound strategy range default false lower ({type 1043 typmod 14 collation 950 value "a\"b"}) upper ({type 1043 typmod 14 collation 950 value "z"})}`
	if got, err := quoted.StructuralOutput(context.Background()); err != nil || got != wantQuoted {
		t.Fatal("captured key usage/escaping", got, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := defaultTree.StructuralOutput(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := (partitionBoundTree{membership: membership}).StructuralOutput(context.Background()); err == nil {
		t.Fatal("wrong bound arity admitted")
	}
}
