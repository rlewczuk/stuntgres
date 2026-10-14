package functions

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBooleanAggregateProvider(t *testing.T) {
	typ, _ := aggregateUnitType(sqltype.Bool)
	for i, p := range []BooleanAggregateProvider{NewBooleanAggregateProvider(typ.Usage(), true), NewBooleanAggregateProvider(typ.Usage(), false)} {
		for _, inputs := range [][]bool{nil, {true}, {false}, {true, true}, {false, false}, {true, false}, {false, true}} {
			state, err := p.Init(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			isolated, err := p.Init(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := i == 0
			for _, v := range inputs {
				if i == 0 {
					want = want && v
				} else {
					want = want || v
				}
				if err := p.Advance(context.Background(), state, sqltype.Boolean(p.ResultUsage().Type, v), sqltype.Value{}); err != nil {
					t.Fatal(err)
				}
			}
			value, err := p.Finalize(context.Background(), state)
			if err != nil {
				t.Fatal(err)
			}
			if len(inputs) == 0 {
				if !value.IsNull() {
					t.Fatal(value)
				}
			} else {
				got, err := value.Boolean()
				if err != nil || got != want {
					t.Fatal(got, want, err)
				}
			}
			value, err = p.Finalize(context.Background(), isolated)
			if err != nil || !value.IsNull() || value.Type() != p.ResultUsage().Type {
				t.Fatal("state leaked", value, err)
			}
			if err := p.Advance(context.Background(), struct{}{}, sqltype.Boolean(p.ResultUsage().Type, true), sqltype.Value{}); err == nil {
				t.Fatal("invalid state accepted")
			}
		}
	}
}
