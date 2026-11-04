package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow Table F.9 and the released intarray implementation, not
// builtin polymorphic array operators. Scalar subtraction is deliberately a bag.
type intarraySetCase struct {
	name, op, rightType, family string
	right                       any
	want                        any
}

var intarraySetCases = []intarraySetCase{
	{"_int_overlap", "&&", "integer[]", "BOOL", "{2,4,2}", true},
	{"_int_contains", "@>", "integer[]", "BOOL", "{3,3,1}", true},
	{"_int_contained", "<@", "integer[]", "BOOL", "{1,2,3,4}", true},
	{"intarray_push_elem", "+", "integer", "_INT4", int64(3), "{3,1,3,2,3}"},
	{"intarray_push_array", "+", "integer[]", "_INT4", "{2,4,2}", "{3,1,3,2,2,4,2}"},
	{"intarray_del_elem", "-", "integer", "_INT4", int64(3), "{1,2}"},
	{"intset_subtract", "-", "integer[]", "_INT4", "{2}", "{1,3}"},
	{"intset_union_elem", "|", "integer", "_INT4", int64(3), "{1,2,3}"},
	{"_int_union", "|", "integer[]", "_INT4", "{2,4,2}", "{1,2,3,4}"},
	{"_int_inter", "&", "integer[]", "_INT4", "{2,4,2}", "{2}"},
}

func (tc intarraySetCase) expression(left, right string, named bool) string {
	if named {
		return "ia_provider." + tc.name + "(" + left + "," + right + ")"
	}
	return left + " OPERATOR(ia_provider." + tc.op + ") " + right
}

func intarraySetLiteral(value any, typ string) string {
	if value == nil {
		return "NULL::" + typ
	}
	if text, ok := value.(string); ok {
		return "'" + strings.ReplaceAll(text, "'", "''") + "'::" + typ
	}
	// Only integer constants in the closed signature matrix reach this path.
	return intarraySetInteger(value.(int64)) + "::" + typ
}

func intarraySetInteger(value int64) string {
	return strconv.FormatInt(value, 10)
}

func intarraySetResult(b fixture.Backend, query, family string, want any, args ...any) fixture.Step {
	return intarraySelect(b, query, []string{"v"}, []string{family}, nil, [][]any{{want}}, args...)
}

func intarraySetNullError() fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("22004", "array must not contain nulls")
}

func intarraySetSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	intarrayInstall(t, p, b, c)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE ia_set_rows(id integer NOT NULL,a integer[],b integer[],d integer[] DEFAULT (ia_provider.intset_union_elem('{3,1,3}'::integer[],2)),CHECK (ia_provider._int_contains(a,'{}'::integer[])))", 0),
		fixture.Exec("INSERT INTO ia_set_rows(id,a,b) VALUES (1,'{3,1,3,2}','{2,4,2}'),(2,'{}','{8,8}'),(3,NULL,NULL)", 3),
		fixture.Exec("CREATE VIEW ia_set_view AS SELECT id,a OPERATOR(ia_provider.&&) b AS overlap,a OPERATOR(ia_provider.+) b AS bag,ia_provider._int_union(a,b) AS united FROM ia_set_rows", 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func intarraySetStoredCheck(b fixture.Backend) fixture.Step {
	return intarraySelect(b, "SELECT id,overlap,bag,united FROM ia_set_view ORDER BY id", []string{"id", "overlap", "bag", "united"}, []string{"INT4", "BOOL", "_INT4", "_INT4"}, []bool{true, true, true, true}, [][]any{
		{int64(1), true, "{3,1,3,2,2,4,2}", "{1,2,3,4}"},
		{int64(2), false, "{8,8}", "{8}"},
		{int64(3), nil, nil, nil},
	})
}

type intarraySetReader struct {
	rows    *sql.Rows
	columns []*sql.ColumnType
	check   fixture.Step
}

func intarraySetRetain(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) intarraySetReader {
	t.Helper()
	check := intarraySetStoredCheck(b)
	rows, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	handedOff := false
	defer func() {
		if !handedOff {
			rows.Close()
		}
	}()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	check.CheckMetadata(t, rows)
	handedOff = true
	return intarraySetReader{rows, columns, check}
}

func intarraySetConsume(t *testing.T, b fixture.Backend, r intarraySetReader) {
	t.Helper()
	var got [][]any
	for r.rows.Next() {
		values := make([]any, len(r.check.Columns))
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := r.rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			fixture.AssertRawValue(t, b.PG, r.check.RawTypes[i], value)
			values[i] = fixture.Normalize(value)
		}
		got = append(got, values)
	}
	if err := r.rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := r.rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, r.check.Rows) {
		t.Fatalf("retained rows %#v want %#v", got, r.check.Rows)
	}
	intarrayMetadata(t, b.PG, r.columns, r.check.Columns, r.check.RawTypes, []bool{true, true, true, true})
}
