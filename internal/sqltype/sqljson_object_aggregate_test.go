package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSQLJSONObjectAggregateUniquenessTiming(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	one, _ := Integer(types[Int4], 1)
	key := String(types[Text], "a\"\\\n")
	for _, id := range []ID{JSON, JSONB} {
		for _, strict := range []bool{false, true} {
			for _, unique := range []bool{false, true} {
				p, err := BindJSONObjectAggregate(types[Text].Usage(), types[Int4].Usage(), types[id].Usage(), strict, unique)
				if err != nil {
					t.Fatal(err)
				}
				s := jsonObjectUnitState(t, p)
				if (s.keys != nil) != (unique && id == JSON) {
					t.Fatal("unneeded key map", id, strict, unique)
				}
				value := one
				if strict {
					value = Null(types[Int4])
				}
				if err = p.Advance(ctx, s, key, value); err != nil {
					t.Fatal(err)
				}
				err = p.Advance(ctx, s, key, value)
				if unique && id == JSON {
					var d *sqlstate.Error
					if !errors.As(err, &d) || d.Code != "22030" || d.Message != `duplicate JSON object key value: "a\"\\\n"` || d.Position != "" {
						t.Fatal("text transition uniqueness", err)
					}
					if out, again := p.Finalize(ctx, s); out != (Value{}) || again != err {
						t.Fatal("text failure not sticky", out, again)
					}
					continue
				}
				if err != nil {
					t.Fatal("binary uniqueness checked before finalization", err)
				}
				if unique {
					// A later NULL key wins over duplicates waiting for binary finalization.
					later := jsonObjectUnitState(t, p)
					for range 2 {
						if err = p.Advance(ctx, later, key, value); err != nil {
							t.Fatal(err)
						}
					}
					err = p.Advance(ctx, later, Null(types[Text]), one)
					var d *sqlstate.Error
					if !errors.As(err, &d) || d.Code != "22023" {
						t.Fatal("later key timing", err)
					}
					if out, again := p.Finalize(ctx, later); out != (Value{}) || again != err {
						t.Fatal("binary key failure not sticky", out, again)
					}
					out, err := p.Finalize(ctx, s)
					if !errors.As(err, &d) || d.Code != "22030" || d.Message != "duplicate JSON object key value" || out != (Value{}) {
						t.Fatal("binary duplicate before null compaction", out, err)
					}
					if out, again := p.Finalize(ctx, s); out != (Value{}) || again != err {
						t.Fatal("binary duplicate not sticky", out, again)
					}
				} else {
					if _, err = p.Finalize(ctx, s); err != nil {
						t.Fatal(err)
					}
				}
				// New execution state never inherits keys or failure from its sibling.
				fresh := jsonObjectUnitState(t, p)
				if err = p.Advance(ctx, fresh, key, one); err != nil {
					t.Fatal(err)
				}
				v, err := p.Finalize(ctx, fresh)
				if err != nil || v.null {
					t.Fatal(v, err)
				}
				if again, err := p.Finalize(ctx, fresh); err != nil || again != v {
					t.Fatal("successful finalization not immutable", again, err)
				}
			}
		}
	}
}

func TestSQLJSONObjectAggregateRootOnlyUniqueness(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	nested, err := types[JSON].Input(ctx, `{"x":1,"x":2}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []ID{JSON, JSONB} {
		p, err := BindJSONObjectAggregate(types[Text].Usage(), types[JSON].Usage(), types[id].Usage(), true, true)
		if err != nil {
			t.Fatal(err)
		}
		s := jsonObjectUnitState(t, p)
		if err = p.Advance(ctx, s, String(types[Text], "a"), nested); err != nil {
			t.Fatal(err)
		}
		want := `{ "a" : {"x":1,"x":2} }`
		if id == JSONB {
			want = `{"a": {"x": 2}}`
		}
		jsonObjectUnitOutput(t, p, s, want)
	}
}

func TestSQLJSONObjectAggregateReachedCancellation(t *testing.T) {
	types := testTypes()
	base := context.Background()
	for _, tc := range []struct {
		id     ID
		target string
		final  bool
	}{
		{JSON, ".writeJSONBString", false},
		{JSONB, ".JSONObjectAggregatePlan.Advance", false},
		{JSONB, ".sortJSONBFields", true},
		{JSONB, ".checkJSONBUniqueKeys", true},
	} {
		p, err := BindJSONObjectAggregate(types[Text].Usage(), types[Text].Usage(), types[tc.id].Usage(), true, true)
		if err != nil {
			t.Fatal(err)
		}
		s := jsonObjectUnitState(t, p)
		ctx, cancel := context.WithCancel(base)
		probe := &jsonAggregateLoopContext{Context: ctx, cancel: cancel, target: tc.target}
		key, value := String(types[Text], "é\"\\"), String(types[Text], "payload")
		if tc.final {
			if err = p.Advance(base, s, key, value); err != nil {
				cancel()
				t.Fatal(err)
			}
			if err = p.Advance(base, s, String(types[Text], "b"), value); err != nil {
				cancel()
				t.Fatal(err)
			}
			out, failure := p.Finalize(probe, s)
			if !probe.entered || !errors.Is(failure, context.Canceled) || out != (Value{}) {
				cancel()
				t.Fatal("finalization cancellation", probe.entered, out, failure)
			}
		} else {
			if err = p.Advance(probe, s, key, value); !probe.entered || !errors.Is(err, context.Canceled) {
				cancel()
				t.Fatal("writer cancellation", probe.entered, err)
			}
		}
		cancel()
		failure := s.failure
		if !errors.Is(failure, context.Canceled) {
			t.Fatal("cancellation was not latched", failure)
		}
		if err := p.Advance(base, s, key, value); err != failure {
			t.Fatal("canceled state resumed advancing", err, failure)
		}
		if out, err := p.Finalize(base, s); !errors.Is(err, context.Canceled) || out != (Value{}) {
			t.Fatal("cancellation published partial value", out, err)
		}
	}
}
