package stuntgres_test

import (
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These are independently specified postassignment values, not TEXT casts of
// the backend result. Date/time strings are the native public adapter contract.
func deparseReviewTypedExpected(t *testing.T, declaration, value string) (string, any) {
	t.Helper()
	base := strings.Split(strings.Fields(declaration)[0], "(")[0]
	typ := map[string]string{"BOOL": "BOOL", "INT": "INT4", "SMALLINT": "INT2", "BIGINT": "INT8", "OID": "OID", "TEXT": "TEXT", "NAME": "NAME", "BPCHAR": "BPCHAR", "CHARACTER": "BPCHAR", "VARCHAR": "VARCHAR", "BYTEA": "BYTEA", "UUID": "UUID", "NUMERIC": "NUMERIC", "REAL": "FLOAT4", "DOUBLE": "FLOAT8", "DATE": "DATE", "TIME": "TIME", "TIMETZ": "TIMETZ", "TIMESTAMP": "TIMESTAMP", "TIMESTAMPTZ": "TIMESTAMPTZ", "INTERVAL": "INTERVAL"}[base]
	if typ == "" {
		t.Fatal("unmapped declaration", declaration)
	}
	switch typ {
	case "BOOL":
		return typ, true
	case "INT2", "INT4", "INT8", "OID":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return typ, n
	case "FLOAT4", "FLOAT8":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			t.Fatal(err)
		}
		return typ, n
	case "BYTEA":
		return typ, []byte{0, 255}
	case "BPCHAR":
		if declaration == "CHARACTER(3)" {
			return typ, "xy "
		}
	case "TIMESTAMP", "TIMESTAMPTZ":
		if value == "infinity" || value == "-infinity" {
			return typ, []byte(value)
		}
		n, err := time.Parse("2006-01-02 15:04:05.999999", strings.TrimSuffix(value, "+00"))
		if err != nil {
			t.Fatal(err)
		}
		return typ, n
	}
	return typ, value
}

func deparseReviewTypedValue(t *testing.T, db *sql.DB, query, declaration, value string) {
	t.Helper()
	typ, want := deparseReviewTypedExpected(t, declaration, value)
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		var stmt *sql.Stmt
		if prepared {
			stmt, err = db.Prepare(query)
			if err == nil {
				rows, err = stmt.Query()
			}
		} else {
			rows, err = db.Query(query)
		}
		if stmt != nil {
			defer stmt.Close()
		}
		if err != nil {
			t.Fatal(query, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != typ {
			t.Fatal(types, typ, err)
		}
		scan := reflect.TypeOf(want)
		if typ == "TIMESTAMP" || typ == "TIMESTAMPTZ" {
			scan = reflect.TypeOf(time.Time{})
		}
		if types[0].ScanType() != scan {
			t.Fatal("ScanType", types[0].ScanType(), scan)
		}
		if !rows.Next() {
			t.Fatal("missing value", rows.Err())
		}
		var got any
		if err := rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
		matches := reflect.DeepEqual(got, want)
		if instant, ok := want.(time.Time); ok {
			actual, ok := got.(time.Time)
			matches = ok && actual.Equal(instant)
		}
		if number, ok := want.(float64); ok {
			actual, ok := got.(float64)
			matches = ok && (actual == number && math.Signbit(actual) == math.Signbit(number) || math.IsNaN(actual) && math.IsNaN(number))
		}
		if !matches {
			t.Fatalf("%s got %T %#v want %T %#v", query, got, got, want, want)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("extra value", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
