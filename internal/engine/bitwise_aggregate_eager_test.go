package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/internal/engine/functions"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestBitwiseAggregateEagerEvaluation(t *testing.T) {
	for _, name := range []string{"bit_and", "bit_or", "bit_xor"} {
		for _, phase := range []string{"success", "late error", "late cancellation"} {
			t.Run(name+"/"+phase, func(t *testing.T) {
				_, q, source := queryCoreFixture(t, "SELECT "+name+"(v) FROM t")
				node := q.Root.(*projectNode).Child.(*aggregateNode)
				usage := node.Calls[0].Argument.ResultUsage()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls, advances := 0, 0
				sentinel := errors.New("late bitwise operand error")
				want := int64(0)
				if name == "bit_or" {
					want = -1
				}
				if name == "bit_xor" {
					want = 7
				}
				node.Calls[0].Argument = queryProbe(usage, func(*EvalContext) (sqltype.Value, error) {
					calls++
					if calls == 4 {
						if phase == "late error" {
							return sqltype.Value{}, sentinel
						}
						if phase == "late cancellation" {
							cancel()
						}
					}
					if calls == 2 {
						return sqltype.Null(usage.Type), nil
					}
					return sqltype.Integer(usage.Type, want)
				})
				original := node.Calls[0].Provider
				node.Calls[0].Provider = aggregateProbeProvider{AggregateProvider: original, advance: func(ctx context.Context, state any, value, separator sqltype.Value) error {
					advances++
					if value.IsNull() {
						t.Fatal("strict runner delivered NULL")
					}
					return original.Advance(ctx, state, value, separator)
				}}
				rows, n, err := finalizeQuery(ctx, q.Root, source, nil)
				if phase == "success" {
					if err != nil || n != 1 || calls != 4 || advances != 3 {
						t.Fatal(n, err, calls, advances)
					}
					defer rows.Close()
					values := make([]sqltype.Value, 1)
					present, err := rows.Next(ctx, values)
					got, inputErr := values[0].Integer()
					if !present || err != nil || inputErr != nil || got != want {
						t.Fatal(got, want, err, inputErr)
					}
				} else {
					expected := error(context.Canceled)
					if phase == "late error" {
						expected = sentinel
					}
					if rows != nil || n != 0 || err != expected || calls != 4 {
						t.Fatal("late error/cancel published output", rows, n, err, calls)
					}
				}
				if source.view != nil || source.anonymous != nil {
					t.Fatal("eager source retained")
				}
			})
		}
	}
}

type bitwiseAggregateCancelContext struct {
	context.Context
	cancel  context.CancelFunc
	target  string
	entered bool
}

func (c *bitwiseAggregateCancelContext) Err() error {
	if !c.entered {
		var pcs [48]uintptr
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
		provider, runner := false, false
		for {
			frame, more := frames.Next()
			provider = provider || strings.HasSuffix(frame.Function, c.target)
			runner = runner || strings.HasSuffix(frame.Function, ".openAggregateNode")
			if !more {
				break
			}
		}
		if provider && runner {
			c.entered = true
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestBitwiseAggregateEagerCancellation(t *testing.T) {
	for _, method := range []string{"Init", "Advance", "Finalize"} {
		t.Run(method, func(t *testing.T) {
			_, q, source := queryCoreFixture(t, "SELECT bit_xor(v) FROM t")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &bitwiseAggregateCancelContext{Context: ctx, cancel: cancel, target: ".IntegralBitwiseAggregateProvider." + method}
			rows, n, err := finalizeQuery(probe, q.Root, source, nil)
			if !probe.entered || rows != nil || n != 0 || err != context.Canceled {
				t.Fatal("actual provider cancellation", probe.entered, rows, n, err)
			}
			if source.view != nil || source.anonymous != nil {
				t.Fatal("canceled source retained")
			}
		})
	}
}

func TestBitwiseAggregateActualBitLoopCancellation(t *testing.T) {
	for _, op := range []functions.AggregateOperation{functions.AggregateBitAnd, functions.AggregateBitOr, functions.AggregateBitXor} {
		e, q, source := queryCoreFixture(t, "SELECT bit_xor(v) FROM t")
		d, _ := e.CatalogRead().LookupType(sqltype.Bit)
		node := q.Root.(*projectNode).Child.(*aggregateNode)
		operator, err := bitwiseAggregateOperator(op)
		if err != nil {
			t.Fatal(err)
		}
		node.Calls[0].Provider = functions.NewBitStringAggregateProvider(d.Usage(), operator)
		// Three polling blocks suffice; the witness is the actual fold frame,
		// not elapsed time, a giant query, or an Err-call count across the pipeline.
		digit := "1"
		if op == functions.AggregateBitAnd {
			digit = "0"
		}
		input, _ := sqltype.BitString(d, strings.Repeat(digit, 8193))
		node.Calls[0].Argument = queryProbe(d.Usage(), func(*EvalContext) (sqltype.Value, error) { return input, nil })
		ctx, cancel := context.WithCancel(context.Background())
		probe := &bitwiseAggregateCancelContext{Context: ctx, cancel: cancel, target: ".BitStringAggregateProvider.fold"}
		rows, n, err := finalizeQuery(probe, q.Root, source, nil)
		cancel()
		if !probe.entered || rows != nil || n != 0 || err != context.Canceled {
			t.Fatal("actual bit loop did not cancel/no-publication", probe.entered, rows, n, err)
		}
		if source.view != nil || source.anonymous != nil {
			t.Fatal("bit-loop source retained")
		}
	}
}

func TestBitwiseAggregateEagerWidthFailure(t *testing.T) {
	for _, op := range []functions.AggregateOperation{functions.AggregateBitAnd, functions.AggregateBitOr, functions.AggregateBitXor} {
		e, q, source := queryCoreFixture(t, "SELECT bit_xor(v) FROM t")
		d, _ := e.CatalogRead().LookupType(sqltype.Bit)
		node := q.Root.(*projectNode).Child.(*aggregateNode)
		operator, err := bitwiseAggregateOperator(op)
		if err != nil {
			t.Fatal(err)
		}
		node.Calls[0].Provider = functions.NewBitStringAggregateProvider(d.Usage(), operator)
		calls := 0
		node.Calls[0].Argument = queryProbe(d.Usage(), func(*EvalContext) (sqltype.Value, error) {
			calls++
			text := "0"
			if calls == 2 {
				text = "1"
			}
			if calls == 3 {
				text = "01"
			}
			return sqltype.BitString(d, text)
		})
		rows, n, err := finalizeQuery(context.Background(), q.Root, source, nil)
		var diagnostic interface{ SQLState() string }
		if rows != nil || n != 0 || !errors.As(err, &diagnostic) || diagnostic.SQLState() != "22026" || calls != 3 {
			t.Fatal("late unequal-width datum did not veto all output", rows, n, err, calls)
		}
		if source.view != nil || source.anonymous != nil {
			t.Fatal("width-error source retained")
		}
	}
}
