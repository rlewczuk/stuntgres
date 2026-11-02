package contrib

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func TestDblinkGetPkeyMetadata(t *testing.T) {
	int4, text := connectbyUnitUsage(sqltype.Int4), connectbyUnitUsage(sqltype.Text)
	members := []sqltype.CompositeMember{{Attribute: 1, Name: "position", Usage: int4}, {Attribute: 2, Name: "colname", Usage: text}}
	result, err := sqltype.Composite(950001, "keys", "keys", 950002, members)
	if err != nil {
		t.Fatal(err)
	}
	relation := &catalog.Relation{Columns: []catalog.Column{{Name: "a", Usage: int4}, {Name: "payload", Usage: text}, {Name: "c", Usage: int4}}, Attributes: []int32{2, 3, 4}, Constraints: catalog.RelationConstraints{Unique: []catalog.UniqueConstraint{{Primary: true, Attributes: []int32{4, 2}}}}}
	before := *relation
	rows, err := dblinkPkeyValues(context.Background(), relation, result)
	if err != nil || len(rows) != 2 || !reflect.DeepEqual(before, *relation) {
		t.Fatal("metadata extraction changed source", rows, err)
	}
	check := func(rows []sqltype.Value, names ...string) {
		t.Helper()
		if len(rows) != len(names) {
			t.Fatal("wrong key width", len(rows))
		}
		for i, name := range names {
			position, err := rows[i].CompositeField(0).Integer()
			label, textErr := rows[i].CompositeField(1).Text()
			if rows[i].Type() != result || position != int64(i+1) || label != name || err != nil || textErr != nil {
				t.Fatal("ordered typed metadata", rows[i], err, textErr)
			}
		}
	}
	check(rows, "c", "a")
	renamed := *relation
	renamed.Columns = append([]catalog.Column(nil), relation.Columns...)
	renamed.Columns[2].Name = "C current"
	fresh, err := dblinkPkeyValues(context.Background(), &renamed, result)
	if err != nil {
		t.Fatal(err)
	}
	check(fresh, "C current", "a")
	check(rows, "c", "a")
	for _, keys := range [][]catalog.UniqueConstraint{nil, {{Attributes: []int32{4, 2}}}} {
		noKey := *relation
		noKey.Constraints.Unique = keys
		empty, err := dblinkPkeyValues(context.Background(), &noKey, result)
		if err != nil || len(empty) != 0 {
			t.Fatal("non-primary key emitted rows", empty, err)
		}
	}
	broken := *relation
	broken.Constraints.Unique = []catalog.UniqueConstraint{{Primary: true, Attributes: []int32{1}}}
	if _, err := dblinkPkeyValues(context.Background(), &broken, result); err == nil {
		t.Fatal("dropped or corrupt key attribute silently omitted")
	}
	record, err := sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []*sqltype.Descriptor{nil, int4.Type, record} {
		if _, err := dblinkPkeyValues(context.Background(), relation, invalid); err == nil {
			t.Fatal("invalid nominal result admitted", invalid)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rows, err := dblinkPkeyValues(ctx, relation, result); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled metadata exposed rows", rows, err)
	}
}
