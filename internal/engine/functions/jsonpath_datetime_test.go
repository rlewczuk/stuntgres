package functions

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type jsonpathDatetimeUnitContext struct {
	*temporalTestContext
	notices []sqlstate.Error
}

func (c *jsonpathDatetimeUnitContext) FunctionJSONPathNotice(n sqlstate.Error) {
	c.notices = append(c.notices, n)
}

func TestJSONPathDatetimeExecutionCapture(t *testing.T) {
	types := temporalTestTypes()
	input := func(id sqltype.ID, text string) sqltype.Value {
		v, err := types[id].Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	values := [4]sqltype.Value{input(sqltype.JSONB, `"2023-08-15 12:34:56.123456+05:30"`), input(sqltype.JSONPath, `$.time_tz(10)`), input(sqltype.JSONB, `{}`), sqltype.Boolean(types[sqltype.Bool], false)}
	e := JSONBPathQueryArray{Info: expr.Info{Usage: types[sqltype.JSONB].Usage()}, WrittenCount: 4, WrittenSlots: [4]uint8{0, 1, 2, 3}}
	var usages [4]sqltype.Usage
	for i, value := range values {
		e.Args[i] = temporalTestLiteral(value)
		usages[i] = value.Type().Usage()
	}
	var err error
	e.Plan, err = sqltype.BindJSONBPathQueryArray(e.Usage, usages[:]...)
	if err != nil {
		t.Fatal(err)
	}
	c := &jsonpathDatetimeUnitContext{temporalTestContext: &temporalTestContext{Context: context.Background()}}
	warning := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIME(10) WITH TIME ZONE precision reduced to maximum allowed, 6"}
	for _, tc := range []struct {
		east int32
		want string
	}{{0, `["07:04:56.123456+00:00"]`}, {36000, `["17:04:56.123456+10:00"]`}, {0, `["07:04:56.123456+00:00"]`}} {
		c.input.Zone = sqltype.FixedZone("unit", tc.east)
		c.notices = nil
		v, err := e.Eval(c)
		if err != nil {
			t.Fatal(err)
		}
		text, err := types[sqltype.JSONB].Output(context.Background(), v)
		if err != nil || text != tc.want {
			t.Fatal(text, tc.want, err)
		}
		if len(c.notices) != 1 || c.notices[0] != warning {
			t.Fatalf("notices=%+v want=%+v", c.notices, warning)
		}
	}
	e.Args[0] = temporalTestLiteral(input(sqltype.JSONB, `[]`))
	c.notices = nil
	v, err := e.Eval(c)
	if err != nil {
		t.Fatal(err)
	}
	text, err := types[sqltype.JSONB].Output(context.Background(), v)
	if err != nil || text != "[]" || len(c.notices) != 0 {
		t.Fatal("empty demand leaked notices", text, err, c.notices)
	}
}
