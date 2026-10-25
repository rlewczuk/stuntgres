package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func commentInquiryMetadata(t *testing.T, rows *sql.Rows, pg, nativeKnown bool, labels []string) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, labels) {
		t.Fatal(names, labels, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(labels) {
		t.Fatal(types, err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "TEXT" || typ.ScanType() != reflect.TypeFor[string]() {
			t.Fatal("TEXT descriptor", typ)
		}
		if n, known := typ.Length(); n != math.MaxInt64 || !known {
			t.Fatal("TEXT length", n, known)
		}
		nullable, known := typ.Nullable()
		if pg {
			if nullable || known {
				t.Fatal("pq unknown nullable", nullable, known)
			}
		} else if nullable != nativeKnown || known != nativeKnown {
			t.Fatal("native TEXT nullable descriptor", nullable, known, "want", nativeKnown, nativeKnown)
		}
	}
}

func commentInquiryRows(t *testing.T, rows *sql.Rows, err error, pg, nativeKnown bool, label string, want []any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	commentInquiryMetadata(t, rows, pg, nativeKnown, []string{label})
	got := []any{}
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, pg, "TEXT", v)
		got = append(got, v)
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("independent TEXT values", got, want, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("nonsticky EOF", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func commentInquiryStep(t *testing.T, p *fixture.Pair, backend fixture.Backend, db fixture.Executor, s fixture.Step) {
	t.Helper()
	allText := len(s.Types) != 0
	for _, typ := range s.Types {
		allText = allText && typ == "TEXT"
	}
	if s.SelectRows && s.State == "" && allText && s.CheckMetadata == nil {
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			// Inquiry functions do not promise a known nullability descriptor.
			commentInquiryMetadata(t, rows, backend.PG, false, s.Columns)
		}
	}
	fixture.RunStep(t, p.Ctx, db, backend.Recorder, s)
}
