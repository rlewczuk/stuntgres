package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func attrsCheckColumns(t *testing.T, columns []*sql.ColumnType, names, types []string, pg, view bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatal("column count", len(columns), names)
	}
	if len(columns) == 14 {
		heapCheckColumns(t, columns[:13], names[:13], types[:13], pg, view)
	}
	array := columns[len(columns)-1]
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if array.Name() != names[len(names)-1] || array.DatabaseTypeName() != "_BYTEA" || array.ScanType() != scan {
		t.Fatal("array metadata", array.Name(), array.DatabaseTypeName(), array.ScanType())
	}
	if n, k := array.Length(); n != 0 || k {
		t.Fatal("array Length", n, k)
	}
	if p, s, k := array.DecimalSize(); p != 0 || s != 0 || k {
		t.Fatal("array DecimalSize", p, s, k)
	}
	want := view && !pg
	if n, k := array.Nullable(); n != want || k != want {
		t.Fatal("array Nullable", n, k, want)
	}
}

func attrsMetadataLifetime(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		attrsCheckColumns(t, columns, s.Columns, s.Types, b.PG, false)
	}
	heapBoth(t, p, b, c, s)
	st, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err := st.QueryContext(p.Ctx, s.Args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	attrsCheckColumns(t, columns, s.Columns, s.Types, b.PG, false)
	count := 0
	for rows.Next() {
		values, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		if count >= len(s.Rows) {
			t.Fatal("unexpected row", values)
		}
		for i, value := range values {
			if !reflect.DeepEqual(fixture.Normalize(value), s.Rows[count][i]) {
				t.Fatal("retained value", i, value, s.Rows[count][i])
			}
			if raw, ok := value.([]byte); ok {
				clear(raw)
			}
		}
		count++
	}
	if rows.Err() != nil || count != len(s.Rows) {
		t.Fatal("cardinality", count, rows.Err())
	}
	attrsCheckColumns(t, columns, s.Columns, s.Types, b.PG, false)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	attrsCheckColumns(t, columns, s.Columns, s.Types, b.PG, false)
	// Exported buffers from the first cursor cannot mutate the next execution.
	heapBoth(t, p, b, c, s)
}

func TestReferencePageinspectHeapAttrsMetadata(t *testing.T) {
	attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		image := attrsImage(attrsBytes("0102030405060708"), 0, 2, nil, true)
		invalid := attrsImage([]byte{}, 0, 0, nil, false)
		heapPointer(invalid, 0, 32767, 1, 24)
		r := make([]any, 14)
		r[0], r[1], r[2], r[3] = int64(1), int64(32767), int64(1), int64(24)
		steps := []fixture.Step{
			attrsRows(attrsSource2, [][]any{attrsRow(8160, 32, 0, 2, 24, nil, attrsArray("01020304", "05060708"))}, image),
			attrsRows(attrsSource2, nil, nil),
			attrsRows(attrsSource2, nil, make([]byte, 24)),
			attrsRows(attrsSource2, [][]any{r}, invalid),
			attrsValue(attrsSplit5, nil, nil, 0, 2, nil),
			attrsValue(attrsSplit5, attrsArray("NULL", "NULL"), []byte{}, 1, 2, "00000000"),
			attrsValue(attrsSplit5, attrsArray("01020304", "NULL"), attrsBytes("01020304"), 0, 1, nil),
		}
		for _, s := range steps {
			attrsMetadataLifetime(t, p, b, c, s)
			s.Query = "SELECT * FROM (" + s.Query + ") AS empty_result WHERE false"
			s.Rows = nil
			attrsMetadataLifetime(t, p, b, c, s)
		}
	})
	t.Run("empty-array", func(t *testing.T) {
		attrsCase(t, "", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			attrsMetadataLifetime(t, p, b, c, attrsValue(attrsSplit5, "{}", []byte{}, 0, 0, nil))
		})
	})
}
