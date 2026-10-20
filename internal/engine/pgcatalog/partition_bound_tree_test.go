package pgcatalog

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPgClassPartitionTree(t *testing.T) {
	integer, ok := builtinType(sqltype.Int4)
	if !ok {
		t.Fatal("INT4")
	}
	key := catalog.PartitionKeyEntry{Usage: integer.Usage()}
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	membership := &catalog.PartitionMembership{Lower: []catalog.PartitionDatum{{Value: value(0)}}, Upper: []catalog.PartitionDatum{{Value: value(10)}}}
	tree := partitionBoundTree{membership: membership, keys: []catalog.PartitionKeyEntry{key}}
	want := `{PartitionBound strategy range default false lower ({type 23 typmod -1 collation 0 value "0"}) upper ({type 23 typmod -1 collation 0 value "10"})}`
	if got, err := tree.StructuralOutput(context.Background()); err != nil || got != want {
		t.Fatal(got, err)
	}
	// Replacing the descriptor's topology does not retarget an already captured tree.
	relation := &catalog.Relation{Membership: membership}
	relation.Membership = &catalog.PartitionMembership{DefaultBound: true}
	if got, err := tree.StructuralOutput(context.Background()); err != nil || got != want {
		t.Fatal("captured tree retargeted", got, err)
	}
	defaultTree := partitionBoundTree{membership: relation.Membership}
	if got, err := defaultTree.StructuralOutput(context.Background()); err != nil || got != "{PartitionBound strategy range default true}" {
		t.Fatal(got, err)
	}
	multi := partitionBoundTree{keys: []catalog.PartitionKeyEntry{key, key}, membership: &catalog.PartitionMembership{Lower: []catalog.PartitionDatum{{Kind: -1}, {Kind: -1}}, Upper: []catalog.PartitionDatum{{Kind: 1}, {Kind: 1}}}}
	if got, err := multi.StructuralOutput(context.Background()); err != nil || got != "{PartitionBound strategy range default false lower (MINVALUE MINVALUE) upper (MAXVALUE MAXVALUE)}" {
		t.Fatal(got, err)
	}
	character, ok := builtinType(sqltype.VarChar)
	if !ok {
		t.Fatal("VARCHAR")
	}
	usage := character.Usage()
	usage.Typmod = 14
	usage.Collation.ID = 950
	quoted := partitionBoundTree{keys: []catalog.PartitionKeyEntry{{Usage: usage}}, membership: &catalog.PartitionMembership{Lower: []catalog.PartitionDatum{{Value: sqltype.String(character, "a\"b")}}, Upper: []catalog.PartitionDatum{{Value: sqltype.String(character, "z")}}}}
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

func builtinType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	for _, d := range sqltype.Builtins() {
		if d.ID() == id {
			return d, true
		}
	}
	return nil, false
}
