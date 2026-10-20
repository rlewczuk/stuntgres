package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/functions"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type anyValueInitProbeProvider struct {
	functions.AggregateProvider
	cancel context.CancelFunc
}

func (p anyValueInitProbeProvider) Init(ctx context.Context) (any, error) {
	state, err := p.AggregateProvider.Init(ctx)
	p.cancel()
	return state, err
}

func TestAnyValueEvaluationAndCancellation(t *testing.T) {
	for _, phase := range []string{"success", "init", "argument", "advance", "finalize", "late argument", "late finalize"} {
		t.Run(phase, func(t *testing.T) {
			qtext := "SELECT any_value(id) FROM t"
			if phase == "late finalize" {
				qtext = "SELECT v,any_value(id) FROM t GROUP BY v"
			}
			_, q, source := queryCoreFixture(t, qtext)
			node := q.Root.(*projectNode).Child.(*aggregateNode)
			usage := node.Calls[0].Argument.ResultUsage()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, advances, finalizations := 0, 0, 0
			sentinel := errors.New("late any_value argument")
			first, _ := sqltype.Integer(usage.Type, 41)
			node.Calls[0].Argument = queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
				calls++
				if phase == "argument" {
					cancel()
				}
				if phase == "late argument" && calls == 4 {
					return sqltype.Value{}, sentinel
				}
				if calls == 2 {
					return sqltype.Null(usage.Type), nil
				}
				if calls == 1 {
					return first, nil
				}
				return sqltype.Integer(usage.Type, 73)
			})
			original := node.Calls[0].Provider
			node.Calls[0].Provider = aggregateProbeProvider{AggregateProvider: original, advance: func(ctx context.Context, state any, v, separator sqltype.Value) error {
				advances++
				if v.IsNull() || v.Type() != usage.Type {
					t.Fatal("transition datum contract", v)
				}
				err := original.Advance(ctx, state, v, separator)
				if phase == "advance" {
					cancel()
				}
				return err
			}, finalize: func(ctx context.Context, state any) (sqltype.Value, error) {
				finalizations++
				value, err := original.Finalize(ctx, state)
				if phase == "finalize" || phase == "late finalize" && finalizations == 2 {
					cancel()
				}
				return value, err
			}}
			if phase == "init" {
				node.Calls[0].Provider = anyValueInitProbeProvider{node.Calls[0].Provider, cancel}
			}
			rows, count, err := finalizeQuery(ctx, q.Root, source, nil)
			if phase == "success" {
				if err != nil || count != 1 || calls != 4 || advances != 3 || finalizations != 1 {
					t.Fatal("retention skipped evaluation", count, err, calls, advances, finalizations)
				}
				defer rows.Close()
				values := make([]sqltype.Value, 1)
				present, err := rows.Next(ctx, values)
				if !present || err != nil || !sqltype.SameDatum(values[0], first) {
					t.Fatal(values, present, err)
				}
				rows.Close()
			} else {
				want := error(context.Canceled)
				if phase == "late argument" {
					want = sentinel
				}
				if rows != nil || count != 0 || err != want {
					t.Fatal("partial aggregate publication", rows, count, err, want)
				}
			}
			if source.view != nil || source.anonymous != nil {
				t.Fatal("retained source owner", source)
			}
		})
	}
}

func TestAnyValueResultUsage(t *testing.T) {
	for _, q := range []string{"SELECT any_value('xy'::varchar(4) COLLATE \"C\") FROM t", "SELECT any_value('xy'::varchar(4) COLLATE \"POSIX\") FROM t", "SELECT any_value(1.2300::numeric(10,4)) FROM t", "SELECT any_value('12:34:56.123'::time(3)) FROM t"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		b.clause.aggregateClause = "SELECT"
		raw := statement(t, q).(*parser.Select).Targets[0]
		result, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(q, err)
		}
		argument := b.grouping.aggregates[0].Argument.ResultUsage()
		usage := result.ResultUsage()
		if usage.Type != argument.Type || usage.Typmod != -1 || usage.Collation != argument.Collation || !result.Properties().StageDependent {
			t.Fatal("result descriptor/typmod/collation", usage, argument, result.Properties())
		}
		input := b.grouping.aggregates[0].Argument
		if result.CollationIndeterminate() != input.CollationIndeterminate() || result.ExplicitCollation() != input.ExplicitCollation() {
			t.Fatal("lost operand collation provenance", result, input)
		}
		if argument.Typmod == -1 {
			t.Fatal("sized argument witness lost typmod", argument)
		}
	}
}
