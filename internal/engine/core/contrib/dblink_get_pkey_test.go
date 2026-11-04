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
	relation := &catalog.Relation{Columns: []catalog.Column{{Name: "a", Usage: int4}, {Name: "payload", Usage: text}, {Name: "c", Usage: int4}}, Attributes: []int32{2, 3, 4}, Constraints: catalog.RelationConstraints{Unique: []catalog.UniqueConstraint{{Primary: true, Attributes: []int32{4, 2}}}}}
	before := *relation
	rows, err := dblinkPkeyNames(context.Background(), relation)
	if err != nil || len(rows) != 2 || !reflect.DeepEqual(before, *relation) {
		t.Fatal("metadata extraction changed source", rows, err)
	}
	check := func(rows []string, names ...string) {
		t.Helper()
		if !reflect.DeepEqual(rows, names) {
			t.Fatalf("ordered key names: got %q want %q", rows, names)
		}
	}
	check(rows, "c", "a")
	renamed := *relation
	renamed.Columns = append([]catalog.Column(nil), relation.Columns...)
	renamed.Columns[2].Name = "C current"
	fresh, err := dblinkPkeyNames(context.Background(), &renamed)
	if err != nil {
		t.Fatal(err)
	}
	check(fresh, "C current", "a")
	check(rows, "c", "a")
	for _, keys := range [][]catalog.UniqueConstraint{nil, {{Attributes: []int32{4, 2}}}} {
		noKey := *relation
		noKey.Constraints.Unique = keys
		empty, err := dblinkPkeyNames(context.Background(), &noKey)
		if err != nil || len(empty) != 0 {
			t.Fatal("non-primary key emitted rows", empty, err)
		}
	}
	broken := *relation
	broken.Constraints.Unique = []catalog.UniqueConstraint{{Primary: true, Attributes: []int32{1}}}
	if _, err := dblinkPkeyNames(context.Background(), &broken); err == nil {
		t.Fatal("dropped or corrupt key attribute silently omitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rows, err := dblinkPkeyNames(ctx, relation); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled metadata exposed rows", rows, err)
	}
}
