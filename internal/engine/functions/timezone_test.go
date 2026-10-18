package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTimezoneLiteralNullExecution(t *testing.T) {
	d := temporalTestTypes()
	for _, tc := range []struct {
		source, result  sqltype.ID
		local, zoneNull bool
	}{
		{sqltype.Timestamp, sqltype.Timestamptz, false, false},
		{sqltype.Timestamptz, sqltype.Timestamp, true, false},
		{sqltype.Timetz, sqltype.Timetz, false, false},
		{sqltype.Timestamp, sqltype.Timestamptz, false, true},
	} {
		value := sqltype.Null(d[tc.source])
		zone := sqltype.String(d[sqltype.Text], "No/Such_Zone")
		if tc.zoneNull {
			value = temporalTestTimestamp(t, d[tc.source], 0)
			zone = sqltype.Null(d[sqltype.Text])
		}
		e := Timezone{Info: expr.Info{Usage: d[tc.result].Usage()}, Value: temporalTestLiteral(value), Zone: temporalTestLiteral(zone), Local: tc.local}
		got, err := e.Eval(&temporalTestContext{Context: context.Background()})
		if err != nil || !got.IsNull() || got.Type() != d[tc.result] {
			t.Fatal(got, err)
		}
	}
}
