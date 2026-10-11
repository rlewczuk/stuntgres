package sqltype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func rangeSetValue(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func rangeSetResult(t *testing.T, p RangeAggregate, values []Value, want string, null bool) Value {
	t.Helper()
	ctx := context.Background()
	s, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if err := p.Advance(ctx, s, v); err != nil {
			t.Fatal(err)
		}
	}
	v, err := p.Finalize(ctx, s)
	if err != nil || v.Type() != p.ResultUsage().Type || v.IsNull() != null {
		t.Fatal("typed final", v, err)
	}
	if !null {
		text, err := v.Type().Output(ctx, v)
		if err != nil || text != want {
			t.Fatalf("set result %q want %q: %v", text, want, err)
		}
	}
	if err := p.Advance(ctx, s, Null(p.input)); err == nil {
		t.Fatal("published state is writable")
	}
	again, err := p.Finalize(ctx, s)
	if err != nil || !SameDatum(v, again) {
		t.Fatal("final is not immutable/idempotent", err)
	}
	return v
}

func TestRangeSetUnion(t *testing.T)        { rangeSetUnits(t, RangeAggregateUnion) }
func TestRangeSetIntersection(t *testing.T) { rangeSetUnits(t, RangeAggregateIntersection) }

func rangeSetUnits(t *testing.T, operation RangeAggregateOperation) {
	ctx := context.Background()
	for _, f := range []struct{ r, m ID }{{Int4Range, Int4Multirange}, {Int8Range, Int8Multirange}, {NumRange, NumMultirange}} {
		for _, input := range []ID{f.r, f.m} {
			t.Run(fmt.Sprint(input), func(t *testing.T) {
				types := testTypes()
				d := types[input]
				p, err := BindRangeAggregate(d, operation)
				if err != nil {
					t.Fatal(err)
				}
				result := types[f.m]
				if operation == RangeAggregateIntersection {
					result = d
				}
				if p.ResultUsage() != result.Usage() || p.ResultUsage().Typmod != -1 || p.ResultUsage().Collation != (CollationRef{}) {
					t.Fatal("same-generation result usage", p.ResultUsage())
				}
				rangeSetResult(t, p, nil, "", true)
				rangeSetResult(t, p, []Value{Null(d), Null(d)}, "", true)
				multi := input == f.m
				for _, tc := range []struct{ a, b, u, i string }{
					{"[1,4)", "[7,9)", "{[1,4),[7,9)}", "empty"},
					{"[1,4)", "[3,7)", "{[1,7)}", "[3,4)"},
					{"empty", "empty", "{}", "empty"},
					{"empty", "[1,4)", "{[1,4)}", "empty"},
					{"(,)", "[1,4)", "{(,)}", "[1,4)"},
				} {
					a, b, intersection := tc.a, tc.b, tc.i
					if multi {
						if a == "empty" {
							a = "{}"
						} else {
							a = "{" + a + "}"
						}
						if b == "empty" {
							b = "{}"
						} else {
							b = "{" + b + "}"
						}
						if intersection == "empty" {
							intersection = "{}"
						} else {
							intersection = "{" + intersection + "}"
						}
					}
					av, bv := rangeSetValue(t, d, a), rangeSetValue(t, d, b)
					want := tc.u
					if operation == RangeAggregateIntersection {
						want = intersection
					}
					originalA, _ := d.Output(ctx, av)
					originalB, _ := d.Output(ctx, bv)
					published := rangeSetResult(t, p, []Value{av, Null(d), bv}, want, false)
					rangeSetResult(t, p, []Value{bv, av}, want, false)
					afterA, _ := d.Output(ctx, av)
					afterB, _ := d.Output(ctx, bv)
					if afterA != originalA || afterB != originalB {
						t.Fatal("mutated operand")
					}
					// Another group's transitions cannot reuse a published member slice.
					duplicate := b
					if !multi && operation == RangeAggregateUnion {
						duplicate = "{" + b + "}"
						if b == "empty" {
							duplicate = "{}"
						}
					}
					rangeSetResult(t, p, []Value{bv, bv}, duplicate, false)
					text, err := published.Type().Output(ctx, published)
					if err != nil || text != want {
						t.Fatal("published payload changed", text, err)
					}
				}
				if multi {
					a := rangeSetValue(t, d, "{[1,4),[7,10)}")
					b := rangeSetValue(t, d, "{[2,3),[8,12)}")
					c := rangeSetValue(t, d, "{[2,4),[9,11)}")
					want := "{[1,4),[7,12)}"
					if operation == RangeAggregateIntersection {
						want = "{[2,3),[9,10)}"
					}
					rangeSetResult(t, p, []Value{a, b, c}, want, false)
					for i, v := range []Value{a, b, c} {
						original := []string{"{[1,4),[7,10)}", "{[2,3),[8,12)}", "{[2,4),[9,11)}"}[i]
						text, err := d.Output(ctx, v)
						if err != nil || text != original {
							t.Fatal("seed/scratch mutated an input", text, err)
						}
					}
				}
				copy := *d
				if _, err := BindRangeAggregate(&copy, operation); err == nil {
					t.Fatal("copied descriptor retained another family's authority")
				}
				foreign := testTypes()[input]
				s, _ := p.Init(ctx)
				if err := p.Advance(ctx, s, Null(foreign)); err == nil {
					t.Fatal("foreign descriptor accepted")
				}
				other, _ := BindRangeAggregate(foreign, operation)
				foreignState, _ := other.Init(ctx)
				if err := p.Advance(ctx, foreignState, Null(d)); err == nil {
					t.Fatal("foreign state accepted")
				}
				if _, err := p.Finalize(ctx, foreignState); err == nil {
					t.Fatal("foreign final accepted")
				}
				otherOperation := RangeAggregateUnion
				if operation == RangeAggregateUnion {
					otherOperation = RangeAggregateIntersection
				}
				other, _ = BindRangeAggregate(d, otherOperation)
				foreignState, _ = other.Init(ctx)
				if _, err := p.Finalize(ctx, foreignState); err == nil {
					t.Fatal("wrong operation state accepted")
				}
			})
		}
	}
	if _, err := BindRangeAggregate(testTypes()[Text], operation); err == nil {
		t.Fatal("nonrange admitted")
	}
	d := testTypes()[NumRange]
	p, _ := BindRangeAggregate(d, operation)
	for _, tc := range []struct{ a, b, u, i string }{
		{"(1,2)", "(2,3)", "{(1,2),(2,3)}", "empty"},
		{"[1,2]", "[2,3]", "{[1,3]}", "[2,2]"},
		{"[1.0,3.00)", "[1.000,4.0)", "{[1.000,4.0)}", "[1.0,3.00)"},
		{"[1.0,4.00)", "[2.000,4.000)", "{[1.0,4.000)}", "[2.000,4.00)"},
		{"[9007199254740993.1,9007199254740993.3)", "[9007199254740993.2,9007199254740993.4)", "{[9007199254740993.1,9007199254740993.4)}", "[9007199254740993.2,9007199254740993.3)"},
	} {
		want := tc.u
		if operation == RangeAggregateIntersection {
			want = tc.i
		}
		rangeSetResult(t, p, []Value{rangeSetValue(t, d, tc.a), rangeSetValue(t, d, tc.b)}, want, false)
	}
	rangeSetCancellation(t, operation)
}

// A deterministic context interrupts actual typed loops, not an SQL test hook.
type rangeSetCancelContext struct {
	context.Context
	remaining int
	failure   error
}

func (c *rangeSetCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return c.failure
	}
	return nil
}

func rangeSetCancellation(t *testing.T, operation RangeAggregateOperation) {
	t.Helper()
	ctx := context.Background()
	d := testTypes()[Int4Multirange]
	p, _ := BindRangeAggregate(d, operation)
	var text strings.Builder
	text.WriteByte('{')
	for i := range 1024 {
		if i > 0 {
			text.WriteByte(',')
		}
		fmt.Fprintf(&text, "[%d,%d)", i*4, i*4+2)
	}
	text.WriteByte('}')
	v := rangeSetValue(t, d, text.String())
	s, _ := p.Init(ctx)
	failure := errors.New("range set interrupted")
	if operation == RangeAggregateIntersection {
		if err := p.Advance(ctx, s, v); err != nil {
			t.Fatal(err)
		}
	}
	active := &rangeSetCancelContext{Context: ctx, remaining: 3, failure: failure}
	if err := p.Advance(active, s, v); err != failure {
		t.Fatal("active transition cancellation identity", err)
	}
	s, _ = p.Init(ctx)
	if err := p.Advance(ctx, s, v); err != nil {
		t.Fatal(err)
	}
	if operation == RangeAggregateUnion {
		active = &rangeSetCancelContext{Context: ctx, remaining: 4, failure: failure}
		result, err := p.Finalize(active, s)
		if err != failure || result.Type() != nil {
			t.Fatal("normalization cancellation published result", result, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Init(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := p.Advance(cancelled, s, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if result, err := p.Finalize(cancelled, s); !errors.Is(err, context.Canceled) || result.Type() != nil {
		t.Fatal(result, err)
	}
}
