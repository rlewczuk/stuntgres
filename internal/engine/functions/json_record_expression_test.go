package functions

import (
	"context"
	"testing"

	"stuntgres/internal/sqltype"
)

type jsonRecordExpressionContext struct{ context.Context }

func (c *jsonRecordExpressionContext) FunctionContext() context.Context { return c.Context }
func (*jsonRecordExpressionContext) FunctionInput() sqltype.InputEnvironment {
	return sqltype.InputEnvironment{}
}

type jsonRecordExpressionArgument struct {
	Info[bool]
	value  sqltype.Value
	visits *int
}

func (e jsonRecordExpressionArgument) Eval(*jsonRecordExpressionContext) (sqltype.Value, error) {
	*e.visits++
	return e.value, nil
}

func TestJSONRecordExpression(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "a", Usage: types[sqltype.Int4].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &jsonRecordExpressionContext{context.Background()}
	input, err := types[sqltype.JSON].Input(ctx, `{"a":2}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	visits := 0
	arg := jsonRecordExpressionArgument{Info: Info[bool]{Usage: types[sqltype.JSON].Usage()}, value: input, visits: &visits}
	e, err := NewJSONRecord[jsonRecordExpressionArgument, *jsonRecordExpressionContext](Info[bool]{Usage: shape.Usage()}, "json_to_record", -1, []jsonRecordExpressionArgument{arg})
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.Eval(ctx)
	if err != nil || visits != 1 || v.Type() != shape {
		t.Fatal(v, visits, err)
	}
	if _, err := e.Reconstruct(e.Info, nil); err == nil {
		t.Fatal("accepted missing argument")
	}
	set, err := NewJSONRecord[jsonRecordExpressionArgument, *jsonRecordExpressionContext](e.Info, "json_to_recordset", -1, []jsonRecordExpressionArgument{arg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.Eval(ctx); err == nil || visits != 1 {
		t.Fatal("evaluated unlifted set", visits, err)
	}
}
