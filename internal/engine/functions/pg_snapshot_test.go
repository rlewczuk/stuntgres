package functions

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSnapshotSource(t *testing.T) {
	types := temporalTestTypes()
	ctx := &temporalTestContext{Context: context.Background()}
	value, err := types[sqltype.PgSnapshot].Input(ctx, "10:20:10,14,15", -1)
	if err != nil {
		t.Fatal(err)
	}
	node := snapshotExpression{Info: expr.Info{Usage: types[sqltype.Xid8].Usage()}, kind: snapshotXip, args: []expr.Expr{temporalTestLiteral(value)}}
	a, err := node.OpenSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := node.OpenSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, cursor := range []*expr.SourceCursor{&a, &b} {
		v, ok, err := cursor.Next(ctx)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		id, err := v.XID8()
		if err != nil || id != 10 {
			t.Fatal(id, err)
		}
	}
	a.Close()
	for _, want := range []uint64{14, 15} {
		v, ok, err := b.Next(ctx)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		id, err := v.XID8()
		if err != nil || id != want {
			t.Fatal(id, err)
		}
	}
	if _, ok, err := b.Next(ctx); err != nil || ok {
		t.Fatal("EOF", ok, err)
	}
	for _, input := range []string{"10:20:", ""} {
		v := sqltype.Null(types[sqltype.PgSnapshot])
		if input != "" {
			v, err = types[sqltype.PgSnapshot].Input(ctx, input, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		empty := node
		empty.args = []expr.Expr{temporalTestLiteral(v)}
		cursor, err := empty.OpenSource(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := cursor.Next(ctx)
		if err != nil || ok || !got.IsNull() || got.Type() != types[sqltype.Xid8] {
			t.Fatal(got, ok, err)
		}
	}
	cursor, err := node.OpenSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := cursor.Next(canceled); !errors.Is(err, context.Canceled) || !cursor.Exhausted || cursor.Value.Type() != nil {
		t.Fatal("canceled source retained payload", err)
	}
	if err := node.ValidateStoredProvider(); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := node.Rebuild(node.Info, node.Children())
	if err != nil {
		t.Fatal(err)
	}
	occurrence := node
	occurrence.kind = snapshotMin
	if !node.SameSourceOccurrence(rebuilt) || node.SameSourceOccurrence(occurrence) || occurrence.SameSourceOccurrence(node) {
		t.Fatal("source occurrence lost its selected set provider")
	}
	recipe, err := rebuilt.(expr.Renderable).Render()
	if err != nil || recipe.Name != "pg_snapshot_xip" {
		t.Fatal(recipe, err)
	}
}
