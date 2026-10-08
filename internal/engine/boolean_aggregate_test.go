package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBooleanAggregateProvider(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT bool_and(f),bool_or(f) FROM t")
	defer source.close()
	node := q.Root.(*projectNode).child.(*aggregateNode)
	for i, p := range []aggregateProvider{node.calls[0].provider, node.calls[1].provider} {
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
				if err := p.Advance(context.Background(), state, sqltype.Boolean(p.ResultUsage().Type, v)); err != nil {
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
			if err := p.Advance(context.Background(), struct{}{}, sqltype.Boolean(p.ResultUsage().Type, true)); err == nil {
				t.Fatal("invalid state accepted")
			}
		}
	}
}

func TestBooleanAggregateSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT count(f),bool_and(f),bool_or(f),every(f),bool_and(f) FROM t")
	defer source.close()
	node := q.Root.(*projectNode).child.(*aggregateNode)
	if len(node.calls) != 3 {
		t.Fatal("operation identity or alias sharing", len(node.calls))
	}
	for i, c := range q.Root.Schema() {
		if !c.NullabilityKnown || c.Nullable != (i != 0) || c.OriginTable != 0 || c.OriginAttribute != 0 || c.Usage.Typmod != -1 {
			t.Fatal(c)
		}
		if i != 0 && c.Usage.Type.ID() != sqltype.Bool {
			t.Fatal(c)
		}
	}
	rows, n, err := finalizeQuery(context.Background(), q.Root, source, nil)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	defer rows.Close()
	values := make([]sqltype.Value, 5)
	present, err := rows.Next(context.Background(), values)
	if !present || err != nil {
		t.Fatal(present, err)
	}
	count, _ := values[0].Integer()
	if count != 3 {
		t.Fatal(count)
	}
	for i, want := range []bool{false, true, false, false} {
		got, err := values[i+1].Boolean()
		if err != nil || got != want {
			t.Fatal(i, got, err)
		}
	}
	// Sequence parameters bind without opening a sequence. Use the admitted
	// scalar-call path to check volatility without executing the fixture.
	_, volatile, owner := queryCoreFixture(t, "SELECT bool_and(nextval($1)>0),every(nextval($1)>0),bool_or(nextval($1)>0) FROM t")
	defer owner.close()
	if len(volatile.Root.(*projectNode).child.(*aggregateNode).calls) != 3 {
		t.Fatal("volatile arguments shared")
	}
}

func TestBooleanAggregateDatumEvaluationAndCancellation(t *testing.T) {
	for _, name := range []string{"bool_and", "bool_or"} {
		for _, phase := range []string{"success", "argument", "advance", "finalize", "late error"} {
			t.Run(name+"/"+phase, func(t *testing.T) {
				_, q, source := queryCoreFixture(t, "SELECT "+name+"(f) FROM t")
				node := q.Root.(*projectNode).child.(*aggregateNode)
				usage := node.calls[0].argument.ResultUsage()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls, advances := 0, 0
				sentinel := errors.New("late Boolean argument error")
				node.calls[0].argument = queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
					calls++
					if phase == "argument" {
						cancel()
					}
					if phase == "late error" && calls == 4 {
						return sqltype.Value{}, sentinel
					}
					if calls == 2 {
						return sqltype.Null(usage.Type), nil
					}
					return sqltype.Boolean(usage.Type, name == "bool_or"), nil
				})
				original := node.calls[0].provider
				node.calls[0].provider = aggregateProbeProvider{aggregateProvider: original, advance: func(ctx context.Context, state any, v sqltype.Value) error {
					advances++
					got, err := v.Boolean()
					if err != nil || got != (name == "bool_or") {
						t.Fatal("datum delivery", v, err)
					}
					err = original.Advance(ctx, state, v)
					if phase == "advance" {
						cancel()
					}
					return err
				}, finalize: func(ctx context.Context, state any) (sqltype.Value, error) {
					v, err := original.Finalize(ctx, state)
					if phase == "finalize" {
						cancel()
					}
					return v, err
				}}
				rows, n, err := finalizeQuery(ctx, q.Root, source, nil)
				if phase == "success" {
					if err != nil || n != 1 || calls != 4 || advances != 3 {
						t.Fatal(n, err, calls, advances)
					}
					values := make([]sqltype.Value, 1)
					present, err := rows.Next(ctx, values)
					got, e := values[0].Boolean()
					if !present || err != nil || e != nil || got != (name == "bool_or") {
						t.Fatal(values, err, e)
					}
					rows.Close()
				} else {
					want := error(context.Canceled)
					if phase == "late error" {
						want = sentinel
					}
					if rows != nil || n != 0 || err != want {
						t.Fatal(rows, n, err, want)
					}
				}
				if source.view != nil || source.anonymous != nil {
					t.Fatal("source owner retained", source)
				}
			})
		}
	}
}
