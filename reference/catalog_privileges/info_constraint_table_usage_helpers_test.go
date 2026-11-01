package reference_test

import (
	"database/sql"
	"reflect"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

var ctuTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}

const ctuQuery = "SELECT * FROM information_schema.constraint_table_usage WHERE table_name LIKE $1 ORDER BY table_catalog,table_schema,table_name,constraint_catalog,constraint_schema,constraint_name"

// Reuse the pinned-session fixture, recording and teardown used by CCU/KCU.
// Each backend has its own subtest, so a native missing view cannot abort PG.
type ctuCase struct{ *ccuCase }

func ctuScenario(t *testing.T, body func(*ctuCase)) {
	t.Helper()
	ccuScenario(t, func(c *ccuCase) { body(&ctuCase{c}) })
}
func (c *ctuCase) row(tableSchema, table, constraintSchema, constraint string) []any {
	return []any{c.database, tableSchema, table, c.database, constraintSchema, constraint}
}
func (c *ctuCase) pub(table, constraint string) []any {
	return c.row("public", table, "public", constraint)
}
func ctuSorted(rows [][]any) [][]any {
	result := append([][]any(nil), rows...)
	sort.SliceStable(result, func(i, j int) bool {
		for n := range fixture.CTUColumns {
			if result[i][n] != result[j][n] {
				return result[i][n].(string) < result[j][n].(string)
			}
		}
		return false
	})
	return result
}
func (c *ctuCase) step(pattern any, rows [][]any) fixture.Step {
	want := ctuSorted(rows)
	s := fixture.Select(ctuQuery, fixture.CTUColumns, ctuTypes, want, pattern)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ctuMetadata(t, r, c.b.PG) }
	s.CheckScan = func(t *testing.T, r *sql.Rows, index int) {
		values, targets := make([]any, 6), make([]any, 6)
		for i := range values {
			targets[i] = &values[i]
		}
		if err := r.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			scan := reflect.TypeOf("")
			if c.b.PG {
				scan = reflect.TypeOf([]byte(nil))
			}
			if value == nil || reflect.TypeOf(value) != scan || fixture.Normalize(value) != want[index][i] {
				t.Fatalf("raw cell row=%d column=%d: %T(%v), want %v(%v)", index, i, value, value, scan, want[index][i])
			}
		}
	}
	return s
}
func (c *ctuCase) check(e fixture.Executor, pattern any, rows ...[]any) {
	c.t.Helper()
	s := c.step(pattern, rows)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func (c *ctuCase) routes(held fixture.HeldStatement, pattern any, rows ...[]any) {
	c.t.Helper()
	c.check(c.conn, pattern, rows...)
	c.check(held, pattern, rows...)
}
func ctuMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, fixture.CTUColumns) {
		t.Fatal("columns", names, "want", fixture.CTUColumns)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	ctuColumnTypes(t, types, pg)
	return types
}
func ctuColumnTypes(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 6 {
		t.Fatal("column types", types)
	}
	for i, typ := range types {
		scan := reflect.TypeOf("")
		if pg {
			scan = reflect.TypeOf((*any)(nil)).Elem()
		}
		nullable, known := typ.Nullable()
		length, hasLength := typ.Length()
		precision, scale, hasDecimal := typ.DecimalSize()
		if typ.Name() != fixture.CTUColumns[i] || typ.DatabaseTypeName() != "NAME" || typ.ScanType() != scan || nullable != !pg || known != !pg || length != 0 || hasLength || precision != 0 || scale != 0 || hasDecimal {
			t.Fatalf("metadata %d: %s %s scan=%v nullable=%v/%v length=%d/%v decimal=%d/%d/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), nullable, known, length, hasLength, precision, scale, hasDecimal)
		}
	}
}
func ctuAssertRows(t *testing.T, rows *sql.Rows, want [][]any, pg bool) {
	t.Helper()
	saved := ctuMetadata(t, rows, pg)
	var got [][]any
	for rows.Next() {
		values, targets := make([]any, 6), make([]any, 6)
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	ctuColumnTypes(t, saved, pg)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	ctuColumnTypes(t, saved, pg)
	if len(got) != len(want) || len(got) > 0 && !reflect.DeepEqual(got, ctuSorted(want)) {
		t.Fatal("rows", got, "want", ctuSorted(want))
	}
}
