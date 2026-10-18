package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

type randomUnitWords struct {
	t     *testing.T
	words []uint64
	at    int
}

func (s *randomUnitWords) Uint64() uint64 {
	if s.at >= len(s.words) {
		s.t.Fatal("unexpected draw")
	}
	word := s.words[s.at]
	s.at++
	return word
}
func TestRandomIntegerSampler(t *testing.T) {
	for _, tc := range []struct {
		lo, hi int64
		words  []uint64
		want   int64
	}{
		{7, 7, nil, 7},
		{-1, 1, []uint64{0, 1}, 0},
		{-1, 1, []uint64{2}, 1},
		{-1, 1, []uint64{3}, -1},
		{-9223372036854775808, 9223372036854775807, []uint64{0}, -9223372036854775808},
		{-9223372036854775808, 9223372036854775807, []uint64{^uint64(0)}, 9223372036854775807},
		{-2, 1, []uint64{3}, 1},
	} {
		source := randomUnitWords{t: t, words: tc.words}
		value := randomInteger(tc.lo, tc.hi, &source)
		if value != tc.want || source.at != len(tc.words) {
			t.Fatal(value, tc.want, source.at)
		}
	}
}

type randomUnitContext struct {
	unitCapabilities
	context.Context
}

func (c *randomUnitContext) FunctionContext() context.Context {
	if c == nil {
		return nil
	}
	return c.Context
}
func (c *randomUnitContext) FunctionRandomOwner() (expr.RandomOwner, error) {
	return nil, fmt.Errorf("stuntgres: missing random session scope")
}

type randomUnitArgument struct {
	expr.Info
	usage sqltype.Usage
	value sqltype.Value
	calls *int
}

func (a randomUnitArgument) ResultUsage() sqltype.Usage { return a.usage }
func (a randomUnitArgument) Eval(expr.Context) (sqltype.Value, error) {
	*a.calls++
	return a.value, nil
}
func TestRandomRuntimeNullEvaluatesSibling(t *testing.T) {
	d := formatUnitTypes()[sqltype.Float8]
	value, err := sqltype.Float(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	first := randomUnitArgument{usage: d.Usage(), value: sqltype.Null(d), calls: &calls}
	last := randomUnitArgument{usage: d.Usage(), value: value, calls: &calls}
	node := Random{Info: expr.Info{Usage: d.Usage()}, Name: "random_normal", Args: []expr.Expr{first, last}}
	result, err := node.Eval(&randomUnitContext{Context: context.Background()})
	if err != nil || !result.IsNull() || result.Type() != d || calls != 2 {
		t.Fatal(result, err, calls)
	}
}
