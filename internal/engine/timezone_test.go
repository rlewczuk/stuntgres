package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestTimezoneBoundSessionSensitiveOperations(t *testing.T) {
	ctx := context.Background()
	e := New()
	ny, err := sqltype.ResolveZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2020, 7, 1, 12, 0, 0, 0, time.UTC)
	for _, query := range []string{
		"SELECT TIMESTAMP '2004-07-01 12:00' AT LOCAL FROM t",
		"SELECT DATE '2004-10-19'::TIMESTAMPTZ FROM t",
		"SELECT TIMESTAMPTZ '2004-10-19 04:00+00'::TIMESTAMP FROM t",
		"SELECT TIMESTAMPTZ '2004-10-19 04:00+00'::TEXT FROM t",
		"SELECT CAST('now' AS TEXT)::TIME FROM t",
		"SELECT DATE '2004-10-19'=TIMESTAMPTZ '2004-10-19 04:00+00' FROM t",
		"SELECT DATE '2004-10-19' IS DISTINCT FROM TIMESTAMPTZ '2004-10-19 04:00+00' FROM t",
	} {
		binder := newExpressionBinder(ctx, e, sqltype.InputEnvironment{TransactionStart: clock}, nil, nil)
		node, err := binder.bind(castTarget(t, query), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(query, err)
		}
		if node.Properties().Volatility != Stable {
			t.Fatal(query, node.Properties())
		}
		evaluation := EvalContext{Context: ctx, Input: sqltype.InputEnvironment{Zone: ny, TransactionStart: clock}, Output: sqltype.OutputEnvironment{Zone: ny}}
		planned, _, err := preflightCoercions(&evaluation, node)
		if err != nil {
			t.Fatal(query, err)
		}
		if _, folded := planned.(literalExpression); folded {
			t.Fatal("session-sensitive operation folded", query)
		}
		if _, err := planned.Eval(&evaluation); err != nil {
			t.Fatal(query, err)
		}
	}
}
func TestTimezonePreparedTemporalPayloads(t *testing.T) {
	ctx := context.Background()
	e := New()
	for _, tc := range []struct{ zone, query, want string }{
		{"America/New_York", "SELECT TIMESTAMPTZ(3) '2004-07-01 12:00' FROM t", "2004-07-01 16:00:00+00"},
		{"UTC167:59:60", "SELECT TIMETZ(3) '12:00' FROM t", "12:00:00-168"},
		{"UTC-167:59:60DST", "SELECT TIMETZ(3) '12:00' FROM t", "12:00:00+169"},
	} {
		zone, err := sqltype.ResolveZone(tc.zone)
		if err != nil {
			t.Fatal(err)
		}
		first := newExpressionBinder(ctx, e, sqltype.InputEnvironment{Zone: zone, TransactionStart: time.Date(2000, 7, 1, 0, 0, 0, 0, time.UTC)}, nil, nil)
		raw := castTarget(t, tc.query)
		original, err := first.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc, err)
		}
		expected := &StatementDescription{temporals: first.temporals}
		second := newExpressionBinder(ctx, e, sqltype.InputEnvironment{TransactionStart: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}, nil, expected)
		replayed, err := second.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc, err)
		}
		a, err := original.Eval(&EvalContext{Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		b, err := replayed.Eval(&EvalContext{Context: ctx})
		if err != nil || !sqltype.SameDatum(a, b) {
			t.Fatal(tc, a, b, err)
		}
		got, err := b.Type().Output(ctx, b)
		if err != nil || got != tc.want || replayed.ResultUsage().Typmod != 3 {
			t.Fatal(tc, got, err)
		}
	}
}
