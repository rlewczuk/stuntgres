package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

func FormatReferenceMetadata(t *testing.T, pg bool, rows *sql.Rows, storedView bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		if column.DatabaseTypeName() != "TEXT" || column.ScanType() != reflect.TypeFor[string]() {
			t.Fatalf("TEXT metadata: %s/%v", column.DatabaseTypeName(), column.ScanType())
		}
		length, lengthKnown := column.Length()
		wantLength := int64(9223372036854775807)
		if !lengthKnown || length != wantLength {
			t.Fatalf("TEXT Length %d/%t, want %d/true", length, lengthKnown, wantLength)
		}
		precision, scale, decimalKnown := column.DecimalSize()
		if precision != 0 || scale != 0 || decimalKnown {
			t.Fatalf("TEXT DecimalSize %d/%d/%t", precision, scale, decimalKnown)
		}
		nullable, nullableKnown := column.Nullable()
		// Native stored view columns have catalog-known nullable descriptors;
		// direct computed format and pq descriptors retain unknown nullability.
		wantNullable := storedView && !pg
		if nullable != wantNullable || nullableKnown != wantNullable {
			t.Fatalf("Nullable %t/%t, want %t/%t (stored view %t, pg %t)", nullable, nullableKnown, wantNullable, wantNullable, storedView, pg)
		}
	}
}
