package exec

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func crosstabUnitTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}
func crosstabUnitShape(t *testing.T, usages ...sqltype.Usage) *sqltype.Descriptor {
	t.Helper()
	members := make([]sqltype.CompositeMember, len(usages))
	for i, u := range usages {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: string(rune('a' + i)), Usage: u}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

func TestTablefuncCrosstabPivot(t *testing.T) {
	types := crosstabUnitTypes()
	text := types[sqltype.Text]
	shape := crosstabUnitShape(t, text.Usage(), text.Usage(), text.Usage())
	str := func(s string) sqltype.Value { return sqltype.String(text, s) }
	null := sqltype.Null(text)
	source := []sqltype.Value{str("a"), null, str("A"), str("a"), str("x"), null, str("a"), str("x"), str("discard"), null, null, str("N"), null, null, str("M"), str("a"), null, str("last")}
	before := append([]sqltype.Value(nil), source...)
	rows, err := pivotCrosstab(context.Background(), source, shape, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source, before) {
		t.Fatal("pivot mutated source")
	}
	want := []string{"(a,A,)", "(,N,M)", "(a,last,)"}
	if len(rows) != len(want) {
		t.Fatal("group count", len(rows))
	}
	for i, row := range rows {
		got, err := shape.Output(context.Background(), row)
		if err != nil || got != want[i] {
			t.Fatal(i, got, err)
		}
	}
	// Numeric output spelling, not SQL equality, defines adjacent groups.
	numeric := types[sqltype.Numeric]
	number := func(s string) sqltype.Value {
		v, err := numeric.Input(context.Background(), s, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	numericShape := crosstabUnitShape(t, numeric.Usage(), text.Usage())
	rows, err = pivotCrosstab(context.Background(), []sqltype.Value{number("1.0"), null, str("one"), number("1.00"), null, str("two")}, numericShape, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil || len(rows) != 2 {
		t.Fatal("numeric spelling collapsed", rows, err)
	}
}

func TestTablefuncCrosstabCompatibility(t *testing.T) {
	types := crosstabUnitTypes()
	state := catalog.NewState(1)
	for _, typ := range types {
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
		state.SetDescriptor(address, &catalog.Type{SQLType: typ})
	}
	name := catalog.TypeDiagnosticNamer(catalog.NewRead(nil, state), nil)
	text, integer, varchar := types[sqltype.Text], types[sqltype.Int4], types[sqltype.VarChar]
	schema := []catalog.Column{{Usage: text.Usage()}, {Usage: integer.Usage()}, {Usage: text.Usage()}}
	for _, tc := range []struct {
		shape  *sqltype.Descriptor
		detail string
	}{
		{crosstabUnitShape(t, text.Usage()), "Return row must have at least two columns."},
		{crosstabUnitShape(t, integer.Usage(), text.Usage()), "Source row_name datatype text does not match return row_name datatype integer."},
		{crosstabUnitShape(t, text.Usage(), text.Usage(), integer.Usage()), "Source value datatype text does not match return value datatype integer in column 3."},
	} {
		var d *sqlstate.Error
		if err := CrosstabCompatibility(schema, tc.shape, name); !errors.As(err, &d) || d.Code != "42804" || d.Detail != tc.detail {
			t.Fatal(err)
		}
	}
	u := varchar.Usage()
	u.Typmod = 7
	schema[0].Usage = u
	schema[2].Usage = u
	if err := CrosstabCompatibility(schema, crosstabUnitShape(t, varchar.Usage(), u), name); err != nil {
		t.Fatal("unconstrained result", err)
	}
	wider := u
	wider.Typmod = 8
	var d *sqlstate.Error
	if err := CrosstabCompatibility(schema, crosstabUnitShape(t, wider, u), name); !errors.As(err, &d) || d.Detail != "Source row_name datatype character varying(3) does not match return row_name datatype character varying(4)." {
		t.Fatal(err)
	}
}

type crosstabUnitCursor struct {
	schema                []catalog.Column
	rows                  [][]sqltype.Value
	at                    int
	closed                bool
	failure, closeFailure error
	cancel                context.CancelFunc
}

func (c *crosstabUnitCursor) Schema() []catalog.Column { return c.schema }
func (c *crosstabUnitCursor) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if c.at == len(c.rows) {
		return false, c.failure
	}
	copy(dst, c.rows[c.at])
	c.at++
	if c.cancel != nil {
		c.cancel()
	}
	return true, nil
}
func (c *crosstabUnitCursor) Close() error { c.closed = true; return c.closeFailure }
func TestTablefuncCrosstabRuntimeCleanup(t *testing.T) {
	types := crosstabUnitTypes()
	text := types[sqltype.Text]
	shape := crosstabUnitShape(t, text.Usage(), text.Usage())
	str := sqltype.String(text, "a")
	primary := errors.New("source error")
	closeErr := errors.New("close error")
	c := &crosstabUnitCursor{schema: []catalog.Column{{Usage: text.Usage()}}, rows: [][]sqltype.Value{{str}}, failure: primary, closeFailure: closeErr}
	values, err := ConsumeCrosstab(context.Background(), c, shape, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, "SELECT source", nil)
	if err != primary || values != nil || !c.closed {
		t.Fatal("original error/close precedence", values, err, c.closed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c = &crosstabUnitCursor{schema: []catalog.Column{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: text.Usage()}}, rows: [][]sqltype.Value{{str, str, str}}, cancel: cancel}
	values, err = ConsumeCrosstab(ctx, c, shape, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, "SELECT source", nil)
	if err != context.Canceled || values != nil || !c.closed {
		t.Fatal("cancellation/close", values, err, c.closed)
	}
	c = &crosstabUnitCursor{schema: []catalog.Column{{Usage: text.Usage()}}}
	values, err = ConsumeCrosstab(context.Background(), c, shape, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, "SELECT source", nil)
	if err != nil || len(values) != 0 || !c.closed {
		t.Fatal("empty source validated width", values, err, c.closed)
	}
}
