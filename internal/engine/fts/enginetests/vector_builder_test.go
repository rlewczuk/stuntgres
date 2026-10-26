package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/fts"
	"testing"
)

func TestFTSConversionPositions(t *testing.T) {
	var b fts.VectorBuilder
	for _, entry := range []struct {
		word     string
		position int
	}{{"rats", 3}, {"the", 1}, {"fat", 2}, {"rats", 3}, {"rats", 4}, {"far", 20000}} {
		b.Add(entry.word, entry.position)
	}
	v, err := b.Finish(context.Background())
	if err != nil || v.String() != "'far':16383 'fat':2 'rats':3,4 'the':1" {
		t.Fatal("direct canonical positions", v, err)
	}
	b.Add("new", 1)
	next, err := b.Finish(context.Background())
	if err != nil || next.String() != "'new':1" || v.String() != "'far':16383 'fat':2 'rats':3,4 'the':1" {
		t.Fatal("builder reused immutable output backing", next, v, err)
	}
	empty, err := b.Finish(context.Background())
	if err != nil || !empty.Valid() || empty.String() != "" {
		t.Fatal("valid empty vector", empty, err)
	}
}
func TestFTSConversionCancellation(t *testing.T) {
	for _, boundary := range []int{1, 2, 5, 20} {
		var b fts.VectorBuilder
		for i := 4095; i >= 0; i-- {
			b.Add(fmt.Sprintf("word%04d", i), i+1)
		}
		ctx := &conversionCancelContext{Context: context.Background(), remaining: boundary}
		if v, err := b.Finish(ctx); !errors.Is(err, context.Canceled) || v != nil {
			t.Fatal("interior vector publication cancellation", boundary, v, err)
		}
	}
}

type conversionCancelContext struct {
	context.Context
	remaining int
}

func (c *conversionCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
