package reference_test

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type copyBinaryProviderExpected struct {
	diagnostic *fixture.Diagnostic
	// Like fixture.Step.PostgresDiagnostic, an explicit pinned-oracle override
	// preserves the independently required native diagnostic without rewriting
	// actual errors or accepting either backend's record indiscriminately.
	postgresDiagnostic          *fixture.Diagnostic
	text, databaseType, rawKind string
	raw                         any
}

// Assert raw public SELECT metadata and shapes BEFORE transport-specific
// normalization. In particular pgx JSON bytes/date/local timestamptz are not
// silently treated as native string/UTC transport values.
func copyBinaryRawProvider(t *testing.T, c copyTextConnection, want copyBinaryProviderExpected) {
	t.Helper()
	databaseType := want.databaseType
	if strings.HasPrefix(databaseType, "$") {
		name := strings.TrimPrefix(databaseType, "$")
		if c.pg {
			databaseType = strconv.FormatInt(int64(copyBinaryTypeOID(t, c, name)), 10)
		} else {
			switch name {
			case "regclass", "regtype", "money", "timetz", "_regclass", "_regtype", "_timetz":
				databaseType = strings.ToUpper(name)
			default:
				// Actual associated array names remain uppercase native metadata;
				// existing enum/composite scalar metadata is deliberately empty.
				databaseType = ""
				if strings.HasPrefix(name, "_") {
					databaseType = strings.ToUpper(name)
				}
			}
		}
	}
	rows, err := c.conn.QueryContext(c.ctx, "SELECT v FROM copy_binary_probe")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"v"}) {
		t.Fatalf("raw columns=%q want=[v]", names)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 || columns[0].DatabaseTypeName() != databaseType {
		t.Fatalf("raw database type=%v want=%q", columns, databaseType)
	}
	if !rows.Next() {
		t.Fatal("missing raw row", rows.Err())
	}
	var actual any
	if err := rows.Scan(&actual); err != nil {
		t.Fatal(err)
	}
	switch want.rawKind {
	case "xid":
		if c.pg {
			if _, ok := actual.(int64); !ok {
				t.Fatalf("pgx raw XID=%T want=int64", actual)
			}
		} else {
			value, ok := actual.([]byte)
			if !ok || value == nil || string(value) != strconv.FormatInt(want.raw.(int64), 10) {
				t.Fatalf("native raw XID=%T %#v want canonical decimal bytes", actual, actual)
			}
			actual = want.raw
		}
	case "date":
		expected := want.raw.(time.Time)
		if c.pg {
			value, ok := actual.(time.Time)
			if !ok || value.Location() != time.UTC {
				t.Fatalf("pgx raw date=%T %#v", actual, actual)
			}
			actual = value
		} else {
			value, ok := actual.(string)
			if !ok || value != expected.Format("2006-01-02") {
				t.Fatalf("native raw date=%T %#v", actual, actual)
			}
			actual = expected
		}
	case "instant":
		value, ok := actual.(time.Time)
		if !ok {
			t.Fatalf("raw timestamp=%T %#v", actual, actual)
		}
		if c.pg && want.databaseType == "TIMESTAMPTZ" {
			if value.Location() != time.Local {
				t.Fatalf("pgx raw timestamptz location=%v want=time.Local", value.Location())
			}
		} else if !c.pg && want.databaseType == "TIMESTAMPTZ" {
			name, offset := value.Zone()
			if name != "UTC" || offset != 0 || value.Location() == time.UTC {
				t.Fatalf("native raw timestamptz location=%v zone=%q offset=%d want=fixed UTC", value.Location(), name, offset)
			}
		} else if value.Location() != time.UTC {
			t.Fatalf("raw timestamp location=%v want=UTC", value.Location())
		}
		actual = value.UTC()
	case "json":
		if c.pg {
			value, ok := actual.([]byte)
			if !ok {
				t.Fatalf("pgx raw JSON=%T %#v", actual, actual)
			}
			actual = string(value)
		} else if _, ok := actual.(string); !ok {
			t.Fatalf("native raw JSON=%T %#v", actual, actual)
		}
	case "string":
		if !c.pg && (want.databaseType == "TIMESTAMP" || want.databaseType == "TIMESTAMPTZ") && (want.raw == "infinity" || want.raw == "-infinity") {
			value, ok := actual.([]byte)
			if !ok {
				t.Fatalf("native raw timestamp infinity=%T want=[]byte", actual)
			}
			actual = string(value)
		} else if _, ok := actual.(string); !ok {
			t.Fatalf("raw value shape=%T want=string", actual)
		}
	default:
		if fmt.Sprintf("%T", actual) != want.rawKind && !(want.rawKind == "null" && actual == nil) {
			t.Fatalf("raw value shape=%T want=%s", actual, want.rawKind)
		}
	}
	equal := reflect.DeepEqual(actual, want.raw)
	if expected, ok := want.raw.(float64); ok {
		value, ok := actual.(float64)
		equal = ok && (math.IsNaN(expected) && math.IsNaN(value) || math.Float64bits(value) == math.Float64bits(expected))
	}
	if !equal {
		t.Errorf("raw value=%#v want=%#v", actual, want.raw)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("raw row multiplicity/end", rows.Err())
	}
}

func copyBinaryProviderWire(t *testing.T, actual, field []byte) {
	t.Helper()
	want := copyBinaryStream(copyBinaryRow(field))
	if !bytes.Equal(actual, want) {
		t.Errorf("binary output=%x want=%x", actual, want)
	}
}
