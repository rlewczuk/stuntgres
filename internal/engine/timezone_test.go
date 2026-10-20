package engine

import (
	"context"
	"stuntgres/internal/engine/expr"
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
		binder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{TransactionStart: clock}, nil)
		node, err := binder.bind(castTarget(t, query), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(query, err)
		}
		if node.Properties().Volatility != expr.Stable {
			t.Fatal(query, node.Properties())
		}
		evaluation := EvalContext{evalStatement: evalStatement{Context: ctx, Input: sqltype.InputEnvironment{Zone: ny, TransactionStart: clock}, Output: sqltype.OutputEnvironment{Zone: ny}}}
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
		first := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{Zone: zone, TransactionStart: time.Date(2000, 7, 1, 0, 0, 0, 0, time.UTC)}, nil)
		raw := castTarget(t, tc.query)
		original, err := first.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc, err)
		}
		expected := &StatementDescription{temporals: first.prepared.temporals}
		second := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{TransactionStart: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}, expected)
		replayed, err := second.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc, err)
		}
		a, err := original.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		b, err := replayed.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		if err != nil || !sqltype.SameDatum(a, b) {
			t.Fatal(tc, a, b, err)
		}
		got, err := b.Type().Output(ctx, b)
		if err != nil || got != tc.want || replayed.ResultUsage().Typmod != 3 {
			t.Fatal(tc, got, err)
		}
	}
}

func TestTimezoneStrictNullAndExpressionOperands(t *testing.T) {
	ctx := context.Background()
	e := New()
	for _, tc := range []struct {
		query string
		id    sqltype.ID
		null  bool
		text  string
	}{
		{"SELECT NULL::TIMESTAMP AT TIME ZONE 'No/Such_Zone' FROM t", sqltype.Timestamptz, true, ""},
		{"SELECT NULL::TIMESTAMPTZ AT LOCAL FROM t", sqltype.Timestamp, true, ""},
		{"SELECT NULL::TIMETZ AT TIME ZONE 'UTC' FROM t", sqltype.Timetz, true, ""},
		{"SELECT TIMESTAMP 'epoch' AT TIME ZONE NULL::TEXT FROM t", sqltype.Timestamptz, true, ""},
		{"SELECT CASE WHEN TRUE THEN TIMETZ '05:34:17-05' ELSE TIMETZ '10:00+00' AT TIME ZONE 'No/Such_Zone' END FROM t", sqltype.Timetz, false, "05:34:17-05"},
		{"SELECT (TIMETZ '05:34:17-05' AT TIME ZONE 'UTC')=TIMETZ '10:34:17+00' FROM t", sqltype.Bool, false, "t"},
		{"SELECT (NULL::TIMESTAMP AT TIME ZONE 'No/Such_Zone') IS DISTINCT FROM NULL::TIMESTAMPTZ FROM t", sqltype.Bool, false, "f"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			binder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			node, err := binder.bind(castTarget(t, tc.query), expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			value, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
			if err != nil {
				t.Fatal(err)
			}
			if node.ResultUsage().Type.ID() != tc.id || value.Type() != node.ResultUsage().Type || value.IsNull() != tc.null {
				t.Fatal(node.ResultUsage(), value)
			}
			if !tc.null {
				text, err := value.Type().Output(ctx, value)
				if err != nil || text != tc.text {
					t.Fatal(text, err)
				}
			}
		})
	}
}

func TestTimezoneCastScopeRetainsCharacterExclusion(t *testing.T) {
	binder := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	_, err := binder.bind(castTarget(t, "SELECT CAST(1 AS varchar(2)) FROM t"), expressionPolicy{context: projectionContext})
	state(t, err, "0A000")
}
