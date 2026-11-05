package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const intarraySortA = "{3,1,1,2}"

// Expectations come from PostgreSQL 18 Table F.8 and REL_18_6 _int_op.c /
// _int_tool.c, not either backend's output. Each documented overload is distinct.
type intarraySortCase struct {
	label, name string
	types       []string
	args        []any
	want        string
}

var intarraySortCases = []intarraySortCase{
	{"sort_unary", "sort", []string{"integer[]"}, []any{intarraySortA}, "{1,1,2,3}"},
	{"sort_binary", "sort", []string{"integer[]", "text"}, []any{intarraySortA, "dEsC"}, "{3,2,1,1}"},
	{"sort_asc", "sort_asc", []string{"integer[]"}, []any{intarraySortA}, "{1,1,2,3}"},
	{"sort_desc", "sort_desc", []string{"integer[]"}, []any{intarraySortA}, "{3,2,1,1}"},
	{"uniq", "uniq", []string{"integer[]"}, []any{intarraySortA}, "{3,1,2}"},
}

func (tc intarraySortCase) expression(args []string) string {
	return "ia_provider." + tc.name + "(" + strings.Join(args, ",") + ")"
}

func (tc intarraySortCase) literals(values []any) []string {
	args := make([]string, len(values))
	for i, value := range values {
		args[i] = intarraySetLiteral(value, tc.types[i])
	}
	return args
}

func (tc intarraySortCase) parameters(typed bool) []string {
	args := make([]string, len(tc.types))
	for i, typ := range tc.types {
		args[i] = "$" + strconv.Itoa(i+1)
		if typed {
			args[i] += "::" + typ
		}
	}
	return args
}

func intarraySortResult(b fixture.Backend, query string, want any, args ...any) fixture.Step {
	return intarraySelect(b, query, []string{"v"}, []string{"_INT4"}, nil, [][]any{{want}}, args...)
}

// Full fields, including absent optional diagnostics, were confirmed against
// the pinned PostgreSQL 18.6 oracle before native implementation.
func intarraySortDirectionError() fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("22023", `second parameter must be "ASC" or "DESC"`)
}

func intarraySortSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	intarrayInstall(t, p, b, c)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE ia_sort_rows(id integer NOT NULL,a integer[],direction text DEFAULT 'ASC',d integer[] DEFAULT ia_provider.sort_desc('{3,1,1,2}'::integer[]),u integer[] DEFAULT ia_provider.uniq(ia_provider.sort('{3,1,1,2}'::integer[])),CHECK (ia_provider.sort(a,direction)=ia_provider.sort(a,'ASC')))", 0),
		fixture.Exec("INSERT INTO ia_sort_rows(id,a) VALUES (1,'{3,1,1,2}'),(2,'{}'),(3,NULL)", 3),
		fixture.Exec("CREATE VIEW ia_sort_view AS SELECT id,ia_provider.sort(a) AS sorted,ia_provider.sort(a,'dEsC') AS directed,ia_provider.sort_asc(a) AS ascending,ia_provider.sort_desc(a) AS descending,ia_provider.uniq(a) AS adjacent FROM ia_sort_rows", 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func intarraySortStoredCheck(b fixture.Backend) fixture.Step {
	return intarraySelect(b, "SELECT id,sorted,directed,ascending,descending,adjacent FROM ia_sort_view ORDER BY id", []string{"id", "sorted", "directed", "ascending", "descending", "adjacent"}, []string{"INT4", "_INT4", "_INT4", "_INT4", "_INT4", "_INT4"}, []bool{true, true, true, true, true, true}, [][]any{
		{int64(1), "{1,1,2,3}", "{3,2,1,1}", "{1,1,2,3}", "{3,2,1,1}", "{3,1,2}"},
		{int64(2), "{}", "{}", "{}", "{}", "{}"},
		{int64(3), nil, nil, nil, nil, nil},
	})
}

type intarraySortReader struct {
	rows    *sql.Rows
	columns []*sql.ColumnType
	check   fixture.Step
}

func intarraySortRetain(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) intarraySortReader {
	t.Helper()
	check := intarraySortStoredCheck(b)
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
	return intarraySortReader{rows, columns, check}
}

func intarraySortConsume(t *testing.T, b fixture.Backend, r intarraySortReader) {
	t.Helper()
	defer r.rows.Close()
	var got [][]any
	for r.rows.Next() {
		values, dest := make([]any, len(r.check.Columns)), make([]any, len(r.check.Columns))
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
	if !reflect.DeepEqual(got, r.check.Rows) {
		t.Fatalf("retained sort rows %#v want %#v", got, r.check.Rows)
	}
	intarrayMetadata(t, b.PG, r.columns, r.check.Columns, r.check.RawTypes, []bool{true, true, true, true, true, true})
}
