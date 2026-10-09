package reference_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReferenceExtremaTypes(t *testing.T) {
	for _, tc := range []struct {
		name, ddl, values, low, high string
	}{
		{"int2", "SMALLINT", "(32767),(NULL),(0),(-32768),(32767)", "-32768", "32767"},
		{"int4", "INTEGER", "(2147483647),(NULL),(0),(-2147483648),(2147483647)", "-2147483648", "2147483647"},
		{"int8", "BIGINT", "(9223372036854775807),(NULL),(9007199254740993),(-9223372036854775808)", "-9223372036854775808", "9223372036854775807"},
		{"oid", "OID", "('4294967295'),(NULL),('2147483648'),('0')", "0", "4294967295"},
		{"float4", "REAL", "('NaN'),('Infinity'),(NULL),(-1),('-Infinity')", "-Infinity", "NaN"},
		{"float8", "DOUBLE PRECISION", "('NaN'),('Infinity'),(NULL),(-1),('-Infinity')", "-Infinity", "NaN"},
		{"numeric", "NUMERIC", "('NaN'),('Infinity'),(NULL),('-123456789012345678901.2300'),('-Infinity')", "-Infinity", "NaN"},
		{"text", "TEXT COLLATE \"C\"", "('é'),(NULL),('a'),('Z'),('é')", "Z", "é"},
		{"varchar", "VARCHAR(4) COLLATE \"C\"", "('z '),(NULL),('a'),('b')", "a", "z "},
		{"bpchar", "CHAR(4)", "('z'),(NULL),('a'),('b')", "a", "z"},
		{"bytea", "BYTEA", "('\\xff'),(NULL),('\\x0000'),('\\x00')", "\\x00", "\\xff"},
		{"date", "DATE", "('infinity'),(NULL),('2000-02-29'),('-infinity')", "-infinity", "infinity"},
		{"time", "TIME", "('24:00'),(NULL),('12:34:56.123'),('00:00')", "00:00:00", "24:00:00"},
		{"timetz", "TIME WITH TIME ZONE", "('12:00+00'),(NULL),('08:00+00'),('09:00+01')", "09:00:00+01", "12:00:00+00"},
		{"timestamp", "TIMESTAMP", "('infinity'),(NULL),('2000-02-29 12:34:56.123456'),('-infinity')", "-infinity", "infinity"},
		{"timestamptz", "TIMESTAMP WITH TIME ZONE", "('infinity'),(NULL),('2000-02-29 12:34:56.123456+01'),('-infinity')", "-infinity", "infinity"},
		{"interval", "INTERVAL", "('31 days'),(NULL),('1 mon'),('30 days'),('-1 day')", "-1 days", "31 days"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extremaReference(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
				run(referenceExec("CREATE TABLE extrema_family(v "+tc.ddl+")", 0))
				run(referenceExec("INSERT INTO extrema_family VALUES "+tc.values, int64(strings.Count(tc.values, "),(")+1)))
				for _, query := range []string{"SELECT min(v)::text,max(v)::text FROM extrema_family", "SELECT min(v)::text,max(v)::text FROM extrema_family WHERE FALSE", "SELECT min(v)::text,max(v)::text FROM extrema_family WHERE v IS NULL"} {
					rows := [][]any{{tc.low, tc.high}}
					if query != "SELECT min(v)::text,max(v)::text FROM extrema_family" {
						rows = [][]any{{nil, nil}}
					}
					t.Run(query, func(t *testing.T) {
						s := referenceSelect(query, []string{"min", "max"}, []string{"TEXT", "TEXT"}, rows)
						if !pg && extremaTextCastExcluded(extremaFamilyType(tc.name)) {
							s = referenceError(query, "0A000")
							s.selectRows = true
						}
						extremaBoth(t, p, conn, recorder, s)
					})
				}
				typ := extremaFamilyType(tc.name)
				raw := []any{extremaRawDatum(t, pg, typ, tc.low), extremaRawDatum(t, pg, typ, tc.high)}
				if typ == "BPCHAR" {
					raw = []any{"a   ", "z   "}
				}
				for _, suffix := range []string{"", " WHERE FALSE", " WHERE v IS NULL"} {
					want := [][]any{raw}
					if suffix != "" {
						want = [][]any{{nil, nil}}
					}
					extremaResult(t, p, conn, recorder, pg, "SELECT min(v),max(v) FROM extrema_family"+suffix, []string{"min", "max"}, []string{typ, typ}, want)
				}
			})
		})
	}
}

func extremaFamilyType(name string) string {
	if name == "varchar" {
		return "TEXT"
	}
	return strings.ToUpper(name)
}

func extremaTextCastExcluded(typ string) bool {
	switch typ {
	case "INT2", "BYTEA", "NUMERIC", "FLOAT4", "FLOAT8", "INTERVAL":
		return true
	}
	return false
}

// Raw contracts use the pinned 18.0 observations and the existing public native
// codecs, not native extrema output. Keep signed zero and exact byte payloads.
func extremaRawDatum(t *testing.T, pg bool, typ, text string) any {
	t.Helper()
	switch typ {
	case "INT2", "INT4", "INT8", "OID":
		if pg && typ == "OID" {
			return []byte(text)
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return n
	case "FLOAT4", "FLOAT8":
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			t.Fatal(err)
		}
		return f
	case "BYTEA":
		b, err := hex.DecodeString(strings.TrimPrefix(text, `\x`))
		if err != nil {
			t.Fatal(err)
		}
		return b
	case "NUMERIC", "INTERVAL", "_NAME", "NAME":
		if pg {
			return []byte(text)
		}
	case "DATE":
		if pg && (text == "infinity" || text == "-infinity") {
			return []byte(text)
		}
	case "TIMESTAMP", "TIMESTAMPTZ":
		if text == "infinity" || text == "-infinity" {
			return []byte(text)
		}
	case "TIME":
		if pg {
			switch text {
			case "00:00:00":
				return time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
			case "24:00:00":
				return time.Date(0, 1, 2, 0, 0, 0, 0, time.UTC)
			case "12:34:56.123":
				return time.Date(0, 1, 1, 12, 34, 56, 123000000, time.UTC)
			}
			t.Fatalf("unspecified TIME witness %q", text)
		}
	case "TIMETZ":
		if pg {
			switch text {
			case "09:00:00+01":
				return time.Date(0, 1, 1, 9, 0, 0, 0, time.FixedZone("", 3600))
			case "12:00:00+00":
				return time.Date(0, 1, 1, 12, 0, 0, 0, time.FixedZone("", 0))
			}
			t.Fatalf("unspecified TIMETZ witness %q", text)
		}
	}
	return text
}

// aggregateColumns optionally limits aggregate-only metadata assertions to a
// leading prefix. TEXT wrappers retain other metadata and raw value checks.
func extremaResult(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool, query string, columns, types []string, raw [][]any, aggregateColumns ...int) {
	t.Helper()
	normalized := make([][]any, len(raw))
	checkMetadata := func(metadata []*sql.ColumnType) {
		t.Helper()
		if len(aggregateColumns) != 0 {
			for _, c := range metadata[aggregateColumns[0]:] {
				if c.ScanType() != reflect.TypeFor[string]() {
					t.Fatalf("%s TEXT ScanType %v, want string", c.Name(), c.ScanType())
				}
				if n, k := c.Length(); n != math.MaxInt64 || !k {
					t.Fatalf("%s TEXT Length %d/%v, want %d/true", c.Name(), n, k, int64(math.MaxInt64))
				}
				if p, s, k := c.DecimalSize(); p != 0 || s != 0 || k {
					t.Fatalf("%s TEXT DecimalSize %d,%d/%v, want 0,0/false", c.Name(), p, s, k)
				}
			}
			metadata = metadata[:aggregateColumns[0]]
		}
		anyValueAssertMetadata(t, pg, metadata)
	}
	for i, row := range raw {
		normalized[i] = make([]any, len(row))
		for j, value := range row {
			normalized[i][j] = referenceNormalize(value)
		}
	}
	// The common step's reflect.DeepEqual cannot compare NaN. The existing
	// owned-row consumer compares floating bits and independently checks raw
	// codecs, metadata and middleware ownership for these exact witnesses.
	hasNaN := false
	for _, row := range raw {
		for _, value := range row {
			if f, ok := value.(float64); ok && math.IsNaN(f) {
				hasNaN = true
			}
		}
	}
	if hasNaN {
		ctx := context.WithValue(p.ctx, referenceRecorderKey{}, recorder)
		for _, prepared := range []bool{false, true} {
			var executor referenceExecutor = conn
			if prepared {
				stmt, err := conn.PrepareContext(ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				executor = referenceHeldStatement{conn, stmt, query}
			}
			rows, err := executor.QueryContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			metadata, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			checkMetadata(metadata)
			got := anyValueConsume(t, rows, recorder, query, columns, types)
			if !anyValueRowsEqual(got, normalized) {
				t.Fatalf("rows %#v, want %#v", got, normalized)
			}
		}
		return
	}
	s := referenceSelect(query, columns, types, normalized)
	s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		checkMetadata(metadata)
	}
	s.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
		got := make([]any, len(types))
		pointers := make([]any, len(types))
		for i := range got {
			pointers[i] = &got[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		if rowIndex >= len(raw) {
			t.Fatalf("unexpected row %d", rowIndex)
		}
		for i, value := range got {
			want := raw[rowIndex][i]
			equal := reflect.DeepEqual(value, want)
			if f, ok := value.(float64); ok {
				w, sameType := want.(float64)
				equal = sameType && (math.IsNaN(f) && math.IsNaN(w) || math.Float64bits(f) == math.Float64bits(w))
			}
			if instant, ok := value.(time.Time); ok {
				w, sameType := want.(time.Time)
				_, zone := instant.Zone()
				_, wantZone := w.Zone()
				equal = sameType && instant.Equal(w) && zone == wantZone
			}
			if !equal {
				t.Fatalf("raw column %d = %T %#v, want %T %#v", i, value, value, want, want)
			}
		}
	}
	extremaBoth(t, p, conn, recorder, s)
}
