package sqltype

import (
	"context"
	"errors"
	"testing"
)

type groupingTree struct {
	text    string
	failure error
}

func (t *groupingTree) StructuralOutput(ctx context.Context) (string, error) {
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

type cancelingGroupingTree struct{ cancel context.CancelFunc }

func (v cancelingGroupingTree) StructuralOutput(context.Context) (string, error) {
	v.cancel()
	return "{CONST 7}", nil
}

func TestTreeOutputCancellationAndConcurrentReaders(t *testing.T) {
	d := testTypes()[PgNodeTree]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	value, err := Tree(d, cancelingGroupingTree{cancel: cancel})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Output(ctx, value); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation during rendering was lost", err)
	}
	if text, err := d.Output(context.Background(), value); err != nil || text != "{CONST 7}" {
		t.Fatal("canceled rendering poisoned retained value", text, err)
	}
	if _, err := d.Output(ctx, value); !errors.Is(err, context.Canceled) {
		t.Fatal("cached text bypassed cancellation", err)
	}
	tree := &groupingTree{text: "{CONST 8}"}
	value, err = Tree(d, tree)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 16)
	for range 16 {
		go func() {
			<-start
			for range 100 {
				text, err := d.Output(context.Background(), value)
				if err != nil {
					results <- err
					return
				}
				if text != "{CONST 8}" {
					results <- errors.New("concurrent reader observed corrupt structural output")
					return
				}
			}
			results <- nil
		}()
	}
	close(start)
	for range 16 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if retained, err := value.Tree(); err != nil || retained != tree {
		t.Fatal("output changed retained structural identity", retained, err)
	}
}
