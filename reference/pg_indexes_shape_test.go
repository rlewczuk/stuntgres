package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

var pgIndexesColumns = []string{"schemaname", "tablename", "indexname", "tablespace", "indexdef"}
var pgIndexesTypes = []string{"NAME", "NAME", "NAME", "NAME", "TEXT"}

const pgIndexesProbe = "SELECT * FROM pg_catalog.pg_indexes WHERE indexname=$1"

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
	pgIndexesColumnMetadata(t, types, pg)
}

// ColumnType values are captured while the cursor is open and remain usable
// independently after catalog mutation and database/sql's automatic EOF close.
func pgIndexesColumnMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
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
		if typ.Name() != pgIndexesColumns[i] || typ.DatabaseTypeName() != pgIndexesTypes[i] || typ.ScanType() != scan || length != wantLength || hasLength != (i == 4) || nullable != !pg || known != !pg || precision != 0 || scale != 0 || decimal {
			t.Fatalf("column %d: name=%s type=%s scan=%v length=%d/%v nullable=%v/%v decimal=%d/%d/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), length, hasLength, nullable, known, precision, scale, decimal)
		}
	}
}
func pgIndexesSelect(b referenceBackend, query string, rows [][]any, args ...any) referenceStep {
	s := referenceSelect(query, pgIndexesColumns, pgIndexesTypes, rows, args...)
	s.checkMetadata = func(t *testing.T, r *sql.Rows) { pgIndexesMetadata(t, r, b.pg) }
	return s
}
func pgIndexesRow(schema, table, index, definition string) []any {
	return []any{schema, table, index, nil, definition}
}
func pgIndexesCheck(t *testing.T, p *referencePair, b referenceBackend, db referenceExecutor, name string, row []any) {
	t.Helper()
	var rows [][]any
	if row != nil {
		rows = [][]any{row}
	}
	referenceRunStep(t, p.ctx, db, b.recorder, pgIndexesSelect(b, pgIndexesProbe, rows, name))
}
func TestReferencePGIndexesShape(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, q := range []string{pgIndexesProbe, "SELECT * FROM pg_catalog.pg_indexes WHERE FALSE", "SELECT * FROM pg_catalog.pg_indexes LIMIT 0"} {
				s := pgIndexesSelect(b, q, nil)
				if q == pgIndexesProbe {
					s.args = []any{"pgi_shape"}
				}
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE pgi_shape_table(n INT)", 0))
			run(referenceExec("CREATE INDEX pgi_shape ON pgi_shape_table(n)", 0))
			row := pgIndexesRow("public", "pgi_shape_table", "pgi_shape", "CREATE INDEX pgi_shape ON public.pgi_shape_table USING btree (n)")
			run(pgIndexesSelect(b, pgIndexesProbe, [][]any{row}, "pgi_shape"))
			run(referencePrepared(pgIndexesSelect(b, pgIndexesProbe, [][]any{row}, "pgi_shape")))
			for _, arg := range []any{"pgi_absent", nil} {
				run(pgIndexesSelect(b, pgIndexesProbe, nil, arg))
				run(referencePrepared(pgIndexesSelect(b, pgIndexesProbe, nil, arg)))
			}
		})
	}
}
