package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCTEDMLMetadata(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "rows", true: "empty"}[empty], func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				cteDMLRun(t, p, b, false, fixture.Exec("CREATE TABLE typed(id INT NOT NULL,n NUMERIC(8,2),v VARCHAR(7),b BOOL)", 0))
				q := "WITH i(k,n,v,b) AS (INSERT INTO typed VALUES(1,12.30,'value',TRUE),(2,NULL,NULL,NULL) RETURNING id,n,v,b) SELECT * FROM i ORDER BY k"
				want := [][]any{{int64(1), "12.30", "value", true}, {int64(2), nil, nil, nil}}
				if empty {
					q += " LIMIT 0"
					want = [][]any{}
				}
				s := fixture.Select(q, []string{"k", "n", "v", "b"}, []string{"INT4", "NUMERIC", "VARCHAR", "BOOL"}, want)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					meta, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					scans := []reflect.Type{reflect.TypeFor[int64](), reflect.TypeFor[string](), reflect.TypeFor[string](), reflect.TypeFor[bool]()}
					if b.PG {
						scans = []reflect.Type{reflect.TypeFor[int32](), reflect.TypeFor[any](), reflect.TypeFor[string](), reflect.TypeFor[bool]()}
					}
					for i, m := range meta {
						if m.ScanType() != scans[i] {
							t.Fatalf("column %d ScanType %v want %v", i, m.ScanType(), scans[i])
						}
						if nullable, known := m.Nullable(); known != !b.PG || (!b.PG && nullable != (i != 0)) {
							t.Fatalf("column %d nullable %v known %v", i, nullable, known)
						}
						length, lengthKnown := m.Length()
						if lengthKnown != (i == 2) || (lengthKnown && length != 7) {
							t.Fatalf("column %d length %d known %v", i, length, lengthKnown)
						}
						precision, scale, decimalKnown := m.DecimalSize()
						if decimalKnown != (i == 1) || (decimalKnown && (precision != 8 || scale != 2)) {
							t.Fatalf("column %d precision %d scale %d known %v", i, precision, scale, decimalKnown)
						}
					}
				}
				cteDMLRun(t, p, b, prepared, s)
				cteDMLRun(t, p, b, false, fixture.Select("SELECT id FROM typed ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			})
		})
	}
}
