package sqltype

import (
	"context"
	"errors"
	"testing"
)

type groupingTree struct {
	text    string
	calls   int
	failure error
}

func (t *groupingTree) StructuralOutput(ctx context.Context) (string, error) {
	t.calls++
	if t.failure != nil {
		return "", t.failure
	}
	return t.text, ctx.Err()
}

func TestGroupingTreePreparedEqualityAndOutput(t *testing.T) {
	d := testTypes()[PgNodeTree]
	ctx := context.Background()
	g, err := BindGrouping(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	left, right := &groupingTree{text: "{CONST 1}"}, &groupingTree{text: "{CONST 1}"}
	a, err := Tree(d, left)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Tree(d, right)
	if err != nil {
		t.Fatal(err)
	}
	if SameDatum(a, b) {
		t.Fatal("fixture accidentally shares payload identity")
	}
	a, err = g.PrepareKey(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	b, err = g.PrepareKey(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		equal, err := g.Equal(ctx, a, b)
		if err != nil || !equal {
			t.Fatal(equal, err)
		}
		ah, err := g.Hash(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		bh, err := g.Hash(ctx, b)
		if err != nil || ah != bh {
			t.Fatal(ah, bh, err)
		}
		out, err := d.Output(ctx, a)
		if err != nil || out != "{CONST 1}" {
			t.Fatal(out, err)
		}
		if again, err := g.PrepareKey(ctx, a); err != nil || again != a {
			t.Fatal(again, err)
		}
	}
	if left.calls != 1 || right.calls != 1 {
		t.Fatal("tree key rerendered during equality/hash/output", left.calls, right.calls)
	}
	if tree, err := a.Tree(); err != nil || tree != left {
		t.Fatal("prepared key lost logical payload", tree, err)
	}
	other, err := Tree(d, &groupingTree{text: "{CONST 2}"})
	if err != nil {
		t.Fatal(err)
	}
	if equal, err := g.Equal(ctx, a, other); err != nil || equal {
		t.Fatal(equal, err)
	}
	if equal, err := g.Equal(ctx, Null(d), Null(d)); err != nil || !equal {
		t.Fatal(equal, err)
	}
}

func TestGroupingTreePreparationFailureAndGeneration(t *testing.T) {
	types := testTypes()
	d := types[PgNodeTree]
	g, _ := BindGrouping(d.Usage())
	sentinel := errors.New("structural output failure")
	value, _ := Tree(d, &groupingTree{failure: sentinel})
	if got, err := g.PrepareKey(context.Background(), value); err != sentinel || got.Type() != nil {
		t.Fatal(got, err)
	}
	foreign, _ := Tree(testTypes()[PgNodeTree], &groupingTree{text: "x"})
	if _, err := g.PrepareKey(context.Background(), foreign); err == nil {
		t.Fatal("foreign structural generation accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.PrepareKey(ctx, value); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
