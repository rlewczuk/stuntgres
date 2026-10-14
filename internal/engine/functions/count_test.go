package functions

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryAggregateCountProvider(t *testing.T) {
	typ, _ := aggregateUnitType(sqltype.Int8)
	p := NewCountProvider(typ.Usage())
	a, err := p.Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.Finalize(context.Background(), a)
	n, _ := v.Integer()
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	for range 2 {
		if err := p.Advance(context.Background(), a, sqltype.Value{}, sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
	}
	v, err = p.Finalize(context.Background(), a)
	n, _ = v.Integer()
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	v, err = p.Finalize(context.Background(), b)
	n, _ = v.Integer()
	if err != nil || n != 0 {
		t.Fatal("state leaked", n, err)
	}
	a.(*countState).count = math.MaxInt64
	max, err := p.Finalize(context.Background(), a)
	maximum, readErr := max.Integer()
	if err != nil || readErr != nil || max.IsNull() || max.Type() != typ || maximum != math.MaxInt64 {
		t.Fatal("max count publication", max, err, readErr)
	}
	var diagnostic *sqlstate.Error
	if err := p.Advance(context.Background(), a, sqltype.Value{}, sqltype.Value{}); !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "bigint out of range"}) {
		t.Fatal(err)
	}
	if a.(*countState).count != math.MaxInt64 {
		t.Fatal("overflow changed count state", a.(*countState).count)
	}
	max, err = p.Finalize(context.Background(), a)
	maximum, readErr = max.Integer()
	if err != nil || readErr != nil || max.IsNull() || max.Type() != typ || maximum != math.MaxInt64 {
		t.Fatal("overflow changed count publication", max, err, readErr)
	}
}
func TestCountProviderOverflow(t *testing.T) {
	typ, _ := aggregateUnitType(sqltype.Int8)
	var diagnostic *sqlstate.Error
	provider := CountProvider{usage: typ.Usage()}
	state, err := provider.Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state.(*countState).count = math.MaxInt64
	err = provider.Advance(context.Background(), state, sqltype.Value{}, sqltype.Value{})
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "bigint out of range"}) {
		t.Fatal(err)
	}
	if state.(*countState).count != math.MaxInt64 {
		t.Fatal("overflow changed count state", state.(*countState).count)
	}
}

func TestCountSignatureFacts(t *testing.T) {
	for _, tc := range []struct {
		name, star bool
		n          int
		want       CountSignature
	}{{true, true, 0, CountCallable}, {true, false, 1, CountCallable}, {true, false, 0, CountRequiresStar}, {false, true, 0, CountMismatch}, {true, false, 2, CountMismatch}, {true, true, 101, CountTooMany}} {
		if got := AnalyzeCountSignature(tc.name, tc.star, tc.n); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
