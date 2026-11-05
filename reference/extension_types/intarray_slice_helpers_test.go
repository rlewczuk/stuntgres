package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const intarraySliceA = "{11,22,33,22}"

// These are independent release expectations, shared by execution routes, not
// values learned from either driver. Each subarray overload is a separate case.
type intarraySliceCase struct {
	name, family string
	types []string
	args []any
	want, changed any
}

var intarraySliceCases = []intarraySliceCase{
	{"icount", "INT4", []string{"integer[]"}, []any{intarraySliceA}, int64(4), int64(1)},
	{"idx", "INT4", []string{"integer[]", "integer"}, []any{intarraySliceA, int64(22)}, int64(2), int64(0)},
	{"subarray", "_INT4", []string{"integer[]", "integer"}, []any{intarraySliceA, int64(2)}, "{22,33,22}", "{}"},
	{"subarray", "_INT4", []string{"integer[]", "integer", "integer"}, []any{intarraySliceA, int64(2), int64(2)}, "{22,33}", "{}"},
	{"intset", "_INT4", []string{"integer"}, []any{int64(-7)}, "{-7}", "{9}"},
	{"prefix", "INT4", []string{"integer[]"}, []any{intarraySliceA}, int64(4), int64(1)},
	{"binary", "INT4", []string{"integer[]", "integer"}, []any{intarraySliceA, int64(22)}, int64(2), int64(0)},
}

func (tc intarraySliceCase) expression(args []string) string {
	switch tc.name {
	case "prefix":
		return "OPERATOR(ia_provider.#) " + args[0]
	case "binary":
		return args[0] + " OPERATOR(ia_provider.#) " + args[1]
	default:
		return "ia_provider." + tc.name + "(" + strings.Join(args, ",") + ")"
	}
}

func (tc intarraySliceCase) parameters(typed bool) []string {
	args := make([]string, len(tc.types))
	for i, typ := range tc.types {
		args[i] = "$" + strconv.Itoa(i+1)
		if typed { args[i] += "::" + typ }
	}
	return args
}

func (tc intarraySliceCase) literals(values []any) []string {
	args := make([]string, len(values))
	for i, value := range values { args[i] = intarraySetLiteral(value, tc.types[i]) }
	return args
}

func intarraySliceResult(b fixture.Backend, query, family string, want any, args ...any) fixture.Step {
	return intarraySelect(b, query, []string{"v"}, []string{family}, nil, [][]any{{want}}, args...)
}

func intarraySliceSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	intarrayInstall(t, p, b, c)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE ia_slice_rows(id integer NOT NULL,a integer[],d integer[] DEFAULT ia_provider.intset(-7),s integer[] DEFAULT ia_provider.subarray('{11,22,33,22}'::integer[],2),CHECK (ia_provider.idx(a,22)>=0))", 0),
		fixture.Exec("INSERT INTO ia_slice_rows(id,a) VALUES (1,'{11,22,33,22}'),(2,'{}'),(3,NULL)", 3),
		fixture.Exec("CREATE VIEW ia_slice_view AS SELECT id,ia_provider.icount(a) AS n,ia_provider.idx(a,22) AS i,ia_provider.subarray(a,2) AS tail,ia_provider.subarray(a,2,2) AS span,ia_provider.intset(id) AS singleton,OPERATOR(ia_provider.#) a AS counted,a OPERATOR(ia_provider.#) 22 AS found FROM ia_slice_rows", 0),
	} { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
}

func intarraySliceStoredCheck(b fixture.Backend) fixture.Step {
	return intarraySelect(b, "SELECT id,n,i,tail,span,singleton,counted,found FROM ia_slice_view ORDER BY id", []string{"id","n","i","tail","span","singleton","counted","found"}, []string{"INT4","INT4","INT4","_INT4","_INT4","_INT4","INT4","INT4"}, []bool{true,true,true,true,true,true,true,true}, [][]any{
		{int64(1),int64(4),int64(2),"{22,33,22}","{22,33}","{1}",int64(4),int64(2)},
		{int64(2),int64(0),int64(0),"{}","{}","{2}",int64(0),int64(0)},
		{int64(3),nil,nil,nil,nil,"{3}",nil,nil},
	})
}

type intarraySliceReader struct {
	rows *sql.Rows
	columns []*sql.ColumnType
	check fixture.Step
}

func intarraySliceRetain(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) intarraySliceReader {
	t.Helper()
	check := intarraySliceStoredCheck(b)
	rows, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil { t.Fatal(err) }
	handedOff := false
	defer func() { if !handedOff { rows.Close() } }()
	columns, err := rows.ColumnTypes()
	if err != nil { rows.Close(); t.Fatal(err) }
	check.CheckMetadata(t, rows)
	handedOff = true
	return intarraySliceReader{rows, columns, check}
}

func intarraySliceConsume(t *testing.T, b fixture.Backend, r intarraySliceReader) {
	t.Helper()
	defer r.rows.Close()
	var got [][]any
	for r.rows.Next() {
		values, dest := make([]any,len(r.check.Columns)), make([]any,len(r.check.Columns))
		for i := range values { dest[i] = &values[i] }
		if err := r.rows.Scan(dest...); err != nil { t.Fatal(err) }
		for i, value := range values {
			fixture.AssertRawValue(t,b.PG,r.check.RawTypes[i],value)
			values[i] = fixture.Normalize(value)
		}
		got = append(got,values)
	}
	if err := r.rows.Err(); err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(got,r.check.Rows) { t.Fatalf("retained rows %#v want %#v",got,r.check.Rows) }
	intarrayMetadata(t,b.PG,r.columns,r.check.Columns,r.check.RawTypes,[]bool{true,true,true,true,true,true,true,true})
}
