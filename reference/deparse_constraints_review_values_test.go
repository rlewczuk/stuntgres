package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Expected postassignment values respect each public adapter's established
// representation. No new SQL conversion is used to make unlike adapters agree.
func deparseReviewTypedExpected(t *testing.T, postgres bool, declaration, value string) (string, any) {
	t.Helper()
	base := strings.Split(strings.Fields(declaration)[0], "(")[0]
	typ := map[string]string{"BOOL": "BOOL", "INT": "INT4", "SMALLINT": "INT2", "BIGINT": "INT8", "OID": "OID", "TEXT": "TEXT", "NAME": "NAME", "BPCHAR": "BPCHAR", "CHARACTER": "BPCHAR", "VARCHAR": "VARCHAR", "BYTEA": "BYTEA", "UUID": "UUID", "NUMERIC": "NUMERIC", "REAL": "FLOAT4", "DOUBLE": "FLOAT8", "DATE": "DATE", "TIME": "TIME", "TIMETZ": "TIMETZ", "TIMESTAMP": "TIMESTAMP", "TIMESTAMPTZ": "TIMESTAMPTZ", "INTERVAL": "INTERVAL"}[base]
	if typ == "" {
		t.Fatal("unmapped declaration", declaration)
	}
	switch typ {
	case "BOOL":
		return typ, true
	case "OID":
		if postgres {
			return typ, value
		}
		fallthrough
	case "INT2", "INT4", "INT8":
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
		return typ, string([]byte{0, 255})
	case "BPCHAR":
		if declaration == "CHARACTER(3)" {
			return typ, "xy "
		}
	case "DATE":
		if postgres && value != "infinity" && value != "-infinity" {
			n, err := time.Parse("2006-01-02", value)
			if err != nil {
				t.Fatal(err)
			}
			return typ, n
		}
	case "TIME":
		return typ, referenceTimeRows(t, postgres, []string{"TIME"}, [][]any{{value}})[0][0]
	case "TIMETZ":
		if postgres {
			n, err := time.Parse("15:04:05.999999Z07", value)
			if err != nil {
				t.Fatal(err)
			}
			return typ, n.UTC()
		}
	case "TIMESTAMP", "TIMESTAMPTZ":
		if value == "infinity" || value == "-infinity" {
			return typ, value
		}
		n, err := time.Parse("2006-01-02 15:04:05.999999", strings.TrimSuffix(value, "+00"))
		if err != nil {
			t.Fatal(err)
		}
		return typ, n
	}
	return typ, value
}

func deparseReviewTypedValue(t *testing.T, conn *sql.Conn, postgres bool, query, declaration, value string) {
	t.Helper()
	typ, want := deparseReviewTypedExpected(t, postgres, declaration, value)
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		var stmt *sql.Stmt
		if prepared {
			stmt, err = conn.PrepareContext(context.Background(), query)
			if err == nil {
				rows, err = stmt.QueryContext(context.Background())
			}
		} else {
			rows, err = conn.QueryContext(context.Background(), query)
		}
		if stmt != nil {
			defer stmt.Close()
		}
		if err != nil {
			t.Fatal(query, err)
		}
		deparseReviewTypedCursor(t, postgres, query, typ, want, rows)
	}
}

func deparseReviewTypedCursor(t *testing.T, postgres bool, query, typ string, want any, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != typ {
		t.Fatal(types, typ, err)
	}
	deparseReviewMetadata(t, postgres, rows)
	if !rows.Next() {
		t.Fatal("missing value", rows.Err())
	}
	var raw any
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	referenceAssertRawValue(t, postgres, typ, raw)
	got := referenceNormalize(raw)
	matches := reflect.DeepEqual(got, want)
	if number, ok := want.(float64); ok {
		actual, ok := got.(float64)
		matches = ok && (actual == number && math.Signbit(actual) == math.Signbit(number) || math.IsNaN(actual) && math.IsNaN(number))
	}
	if !matches {
		t.Fatalf("%s postgres=%t got %T %#v want %T %#v", query, postgres, got, got, want, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra value", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

// Preserve the original DB.Prepare/Stmt.Query and DB.Query consumers alongside
// the Context inquiry consumers, with the same typed-value comparator.
func deparseReviewSourceTypedValue(t *testing.T, db *sql.DB, postgres bool, query, declaration, value string) {
	t.Helper()
	typ, want := deparseReviewTypedExpected(t, postgres, declaration, value)
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var stmt *sql.Stmt
		var err error
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
		deparseReviewTypedCursor(t, postgres, query, typ, want, rows)
	}
}
