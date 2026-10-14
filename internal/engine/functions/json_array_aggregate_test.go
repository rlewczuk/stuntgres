package functions

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func jsonArrayProviderUnit(t *testing.T, input, result sqltype.Usage, absent bool) JSONArrayAggregateProvider {
	t.Helper()
	plan, err := sqltype.BindJSONArrayAggregate(input, result, absent)
	if err != nil {
		t.Fatal(err)
	}
	return JSONArrayAggregateProvider{input: input, usage: result, absent: absent, plan: plan}
}
func TestJSONArrayAggregateProviderEnvironment(t *testing.T) {
	ctx := context.Background()
	bytes, _ := aggregateUnitType(sqltype.Bytea)
	result, _ := aggregateUnitType(sqltype.JSON)
	p := jsonArrayProviderUnit(t, bytes.Usage(), result.Usage(), false)
	value, err := sqltype.Binary(bytes, "AB")
	if err != nil {
		t.Fatal(err)
	}
	hex, err := p.InitWithEnvironment(ctx, sqltype.OutputEnvironment{ByteaOutput: sqltype.ByteaHex})
	if err != nil {
		t.Fatal(err)
	}
	escape, err := p.InitWithEnvironment(ctx, sqltype.OutputEnvironment{ByteaOutput: sqltype.ByteaEscape})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []any{hex, escape} {
		if err = p.Advance(ctx, state, value, sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		state any
		want  string
	}{{hex, `["\\x4142"]`}, {escape, `["AB"]`}} {
		v, err := p.Finalize(ctx, tc.state)
		if err != nil {
			t.Fatal(err)
		}
		text, outputErr := v.Type().Output(ctx, v)
		if outputErr != nil || text != tc.want || v.Type() != result {
			t.Fatal("execution environment shared", text, tc.want, outputErr)
		}
	}
	if err = p.Advance(ctx, struct{}{}, value, sqltype.Value{}); err == nil {
		t.Fatal("foreign state accepted")
	}
	if _, err = p.Finalize(ctx, nil); err == nil {
		t.Fatal("nil state accepted")
	}
}
