package fixture

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

var pgIndexesColumns = []string{"schemaname", "tablename", "indexname", "tablespace", "indexdef"}

var PgIndexesTypes = []string{"NAME", "NAME", "NAME", "NAME", "TEXT"}

const PgIndexesProbe = "SELECT * FROM pg_catalog.pg_indexes WHERE indexname=$1"

func pgIndexesMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cols, pgIndexesColumns) || len(types) != 5 {
		t.Fatal(cols, types)
	}
	PgIndexesColumnMetadata(t, types, pg)
}

// ColumnType values are captured while the cursor is open and remain usable
// independently after catalog mutation and database/sql's automatic EOF close.
func PgIndexesColumnMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 5 {
		t.Fatal("saved descriptor width", types)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		if pg && i < 4 {
			scan = reflect.TypeFor[any]()
		}
		length, hasLength := typ.Length()
		nullable, known := typ.Nullable()
		precision, scale, decimal := typ.DecimalSize()
		wantLength := int64(0)
		if i == 4 {
			wantLength = math.MaxInt64
		}
		if typ.Name() != pgIndexesColumns[i] || typ.DatabaseTypeName() != PgIndexesTypes[i] || typ.ScanType() != scan || length != wantLength || hasLength != (i == 4) || nullable != !pg || known != !pg || precision != 0 || scale != 0 || decimal {
			t.Fatalf("column %d: name=%s type=%s scan=%v length=%d/%v nullable=%v/%v decimal=%d/%d/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), length, hasLength, nullable, known, precision, scale, decimal)
		}
	}
}

func PgIndexesSelect(b Backend, query string, rows [][]any, args ...any) Step {
	s := Select(query, pgIndexesColumns, PgIndexesTypes, rows, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { pgIndexesMetadata(t, r, b.PG) }
	return s
}

func PgIndexesRow(schema, table, index, definition string) []any {
	return []any{schema, table, index, nil, definition}
}

func PgIndexesCheck(t *testing.T, p *Pair, b Backend, db Executor, name string, row []any) {
	t.Helper()
	var rows [][]any
	if row != nil {
		rows = [][]any{row}
	}
	RunStep(t, p.Ctx, db, b.Recorder, PgIndexesSelect(b, PgIndexesProbe, rows, name))
}
