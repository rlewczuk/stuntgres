package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func heapCheckColumns(t *testing.T, columns []*sql.ColumnType, names, types []string, pg, view bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatal("column count", len(columns), names)
	}
	for i, c := range columns {
		if c.Name() != names[i] || c.DatabaseTypeName() != types[i] {
			t.Fatal(i, c.Name(), c.DatabaseTypeName(), names[i], types[i])
		}
		var scan reflect.Type
		switch types[i] {
		case "INT2":
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int16]()
			}
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		case "OID":
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[any]()
			}
		case "XID":
			scan = reflect.TypeFor[[]byte]()
			if pg {
				scan = reflect.TypeFor[any]()
			}
		case "TID", "_TEXT", "RECORD":
			scan = reflect.TypeFor[string]()
			if pg {
				scan = reflect.TypeFor[any]()
			}
		case "TEXT":
			scan = reflect.TypeFor[string]()
		case "BYTEA":
			scan = reflect.TypeFor[[]byte]()
		default:
			t.Fatal("unmapped heap type", types[i])
		}
		if c.ScanType() != scan {
			t.Fatal(i, "ScanType", c.ScanType(), scan)
		}
		nullable, known := c.Nullable()
		want := view && !pg
		if nullable != want || known != want {
			t.Fatal(i, "Nullable", nullable, known, "want", want, want)
		}
		length, lengthKnown := int64(0), false
		// Both adapters advertise unbounded TEXT; only pq does so for BYTEA.
		// Native BYTEA retains Usage.Length's noncharacter unknown contract.
		if types[i] == "TEXT" || pg && types[i] == "BYTEA" {
			length, lengthKnown = math.MaxInt64, true
		}
		if n, k := c.Length(); n != length || k != lengthKnown {
			t.Fatal(i, "Length", n, k, "want", length, lengthKnown)
		}
		if p, s, k := c.DecimalSize(); p != 0 || s != 0 || k {
			t.Fatal(i, "DecimalSize", p, s, k)
		}
	}
}

func heapMetadata(s fixture.Step, pg, view bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		c, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		heapCheckColumns(t, c, s.Columns, s.Types, pg, view)
	}
	return s
}

func heapRawCell(t *testing.T, typ string, v any, pg bool) {
	t.Helper()
	if v == nil {
		return
	}
	switch typ {
	case "INT2", "INT4":
		if _, ok := v.(int64); !ok {
			t.Fatalf("raw %s: %T", typ, v)
		}
	case "OID":
		if pg {
			if _, ok := v.([]byte); !ok {
				t.Fatalf("raw OID: %T", v)
			}
		} else if _, ok := v.(int64); !ok {
			t.Fatalf("raw OID: %T", v)
		}
	case "XID", "BYTEA":
		if raw, ok := v.([]byte); !ok || raw == nil {
			t.Fatalf("raw nonnull %s: %T %v", typ, v, v)
		}
	case "TID", "_TEXT", "RECORD":
		if pg {
			if _, ok := v.([]byte); !ok {
				t.Fatalf("raw %s: %T", typ, v)
			}
		} else if _, ok := v.(string); !ok {
			t.Fatalf("raw %s: %T", typ, v)
		}
	case "TEXT":
		if _, ok := v.(string); !ok {
			t.Fatalf("raw TEXT: %T", v)
		}
	}
}

func heapMetadataLifetime(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, steps []fixture.Step) {
	t.Helper()
	st, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, s := range steps {
		heapBoth(t, p, b, c, heapMetadata(s, b.PG, false))
		rows, err := st.QueryContext(p.Ctx, s.Args...)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		heapCheckColumns(t, columns, s.Columns, s.Types, b.PG, false)
		count := 0
		for rows.Next() {
			values := make([]any, len(s.Columns))
			dest := make([]any, len(values))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if count >= len(s.Rows) {
				rows.Close()
				t.Fatal("unexpected row", values)
			}
			for i, v := range values {
				heapRawCell(t, s.Types[i], v, b.PG)
				if !reflect.DeepEqual(fixture.Normalize(v), s.Rows[count][i]) {
					rows.Close()
					t.Fatal("raw value", count, i, v, s.Rows[count][i])
				}
			}
			count++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if count != len(s.Rows) {
			rows.Close()
			t.Fatal("cardinality", count, len(s.Rows))
		}
		heapCheckColumns(t, columns, s.Columns, s.Types, b.PG, false)
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		heapCheckColumns(t, columns, s.Columns, s.Types, b.PG, false)
	}
}

func TestReferencePageinspectHeapMetadata(t *testing.T) {
	heapCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Explicit hex output preserves nonnil empty bytea on pq as well as native.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET bytea_output TO hex", 0))
		invalid, invalidRow := heapImage(), make([]any, 14)
		heapPointer(invalid, 0, 32, 1, 0)
		invalidRow[0], invalidRow[1], invalidRow[2], invalidRow[3] = int64(1), int64(32), int64(1), int64(0)
		empty, emptyRow := heapImage()[:64], heapRow(b.PG)
		heapPointer(empty, 0, 32, 1, 32)
		emptyRow[3], emptyRow[13] = int64(32), ""
		source := []fixture.Step{heapStep(heapSource, heapImage(), heapRow(b.PG)), heapStep(heapSource, nil), heapStep(heapSource, make([]byte, 24)), heapStep(heapSource, invalid, invalidRow), heapStep(heapSource, empty, emptyRow)}
		flags := []fixture.Step{heapFlagsStep(heapFlagsSource, int64(1), int64(0), "{HEAP_HASNULL}", "{}"), heapFlagsStep(heapFlagsSource, nil, int64(0), nil, nil)}
		scalar := []fixture.Step{fixture.Select(heapFlagsScalar, []string{"heap_tuple_infomask_flags"}, []string{"RECORD"}, [][]any{{"({},{})"}}, int64(0), int64(0)), fixture.Select(heapFlagsScalar, []string{"heap_tuple_infomask_flags"}, []string{"RECORD"}, [][]any{{nil}}, nil, int64(0))}
		heapMetadataLifetime(t, p, b, c, heapSource, source)
		heapMetadataLifetime(t, p, b, c, heapFlagsSource, flags)
		heapMetadataLifetime(t, p, b, c, heapFlagsScalar, scalar)
		for _, s := range append(append(source, flags...), scalar...) {
			s.Query += " WHERE false"
			s.Rows = nil
			heapMetadataLifetime(t, p, b, c, s.Query, []fixture.Step{s})
		}
	})
}
